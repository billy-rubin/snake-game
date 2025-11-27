package application

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"snake-game/internal/domain"
	"snake-game/internal/infrastracture/network"
)

type GameClient struct {
	conn       *network.UnicastConn
	serverAddr *net.UDPAddr
	log        *log.Logger

	gameName   string
	playerName string
	playerType domain.PlayerType
	reqRole    domain.NodeRole

	myID    int32
	nodeID  int32
	nextSeq int64

	cfg            *domain.GameConfig
	state          *domain.GameState
	lastStateOrder int32
}

func NewGameClient(
	serverAddr *net.UDPAddr,
	gameName string,
	playerName string,
	pType domain.PlayerType,
	role domain.NodeRole,
	logger *log.Logger,
) (*GameClient, error) {
	if serverAddr == nil {
		return nil, fmt.Errorf("serverAddr is nil")
	}
	if logger == nil {
		logger = log.Default()
	}

	conn, err := network.NewUnicastConn(nil)
	if err != nil {
		return nil, fmt.Errorf("create unicast conn: %w", err)
	}

	return &GameClient{
		conn:       conn,
		serverAddr: serverAddr,
		log:        logger,
		gameName:   gameName,
		playerName: playerName,
		playerType: pType,
		reqRole:    role,
		nodeID:     0,
		nextSeq:    1,
	}, nil
}

func (c *GameClient) incrementNextSeq() int64 {
	c.nextSeq++
	return c.nextSeq
}

func (c *GameClient) JoinOnce() error {
	seq := c.incrementNextSeq()

	joinMsg := domain.NewJoinMessage(
		seq,
		c.nodeID,
		c.playerType,
		c.playerName,
		c.gameName,
		c.reqRole,
	)

	if err := c.conn.Send(joinMsg, c.serverAddr); err != nil {
		return fmt.Errorf("send Join: %w", err)
	}
	c.log.Printf("JOIN sent seq=%d to %s", seq, c.serverAddr)

	gotAck := false
	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		env, err := c.conn.ReceiveOneWithTimeout(500 * time.Millisecond)
		if err != nil {
			continue
		}
		if env == nil || env.Msg == nil {
			continue
		}

		msg := env.Msg
		switch t := msg.Type.(type) {
		case *domain.GameMessage_Ack:
			if msg.ReceiverId != nil {
				c.myID = msg.GetReceiverId()
				c.nodeID = c.myID
			}
			gotAck = true
			c.log.Printf("ACK received. My PlayerID = %d", c.myID)

		case *domain.GameMessage_Error:
			return fmt.Errorf("server rejected join: %s", t.Error.GetErrorMessage())

		case *domain.GameMessage_Announcement:
			ann := t.Announcement
			if ann != nil {
				for _, g := range ann.GetGames() {
					if g.GetGameName() == c.gameName {
						c.cfg = g.GetConfig()
					}
				}
			}
		}

		if gotAck {
			break
		}
	}

	if !gotAck {
		return fmt.Errorf("no Ack for Join")
	}
	if c.cfg == nil {
		c.cfg = &domain.GameConfig{}
	}
	return nil
}

func (c *GameClient) RunGame() error {
	app := tview.NewApplication()

	textView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false).
		SetChangedFunc(func() {
			app.Draw()
		})

	textView.SetBorder(true).
		SetTitle(fmt.Sprintf(" Game: %s ", c.gameName))

	textView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if c.reqRole == domain.NodeRole_VIEWER || c.playerType == domain.PlayerType_ROBOT {
			if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
				app.Stop()
				return nil
			}
			return event
		}

		var dir domain.Direction
		switch event.Key() {
		case tcell.KeyUp:
			dir = domain.Direction_UP
		case tcell.KeyDown:
			dir = domain.Direction_DOWN
		case tcell.KeyLeft:
			dir = domain.Direction_LEFT
		case tcell.KeyRight:
			dir = domain.Direction_RIGHT
		case tcell.KeyEsc:
			app.Stop()
			return nil
		}

		switch event.Rune() {
		case 'w', 'W':
			dir = domain.Direction_UP
		case 's', 'S':
			dir = domain.Direction_DOWN
		case 'a', 'A':
			dir = domain.Direction_LEFT
		case 'd', 'D':
			dir = domain.Direction_RIGHT
		case 'q', 'Q':
			app.Stop()
			return nil
		}

		if dir != 0 {
			go c.sendSteer(dir)
			return nil
		}
		return event
	})

	app.SetRoot(textView, true)

	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		c.recvLoop(stopCh, app, textView)
	}()

	err := app.Run()
	close(stopCh)
	wg.Wait()
	_ = c.conn.Close()

	return err
}

func (c *GameClient) sendSteer(d domain.Direction) {
	msg := domain.NewSteerMessage(c.incrementNextSeq(), c.myID, d)
	if err := c.conn.Send(msg, c.serverAddr); err != nil {
		c.log.Printf("failed to send steer: %v", err)
	}
}

func (c *GameClient) recvLoop(stopCh <-chan struct{}, app *tview.Application, tv *tview.TextView) {
	for {
		select {
		case <-stopCh:
			return
		default:
		}

		env, err := c.conn.ReceiveOneWithTimeout(500 * time.Millisecond)
		if err != nil {
			continue
		}
		if env == nil || env.Msg == nil {
			continue
		}

		stMsg := env.Msg.GetState()
		if stMsg == nil {
			continue
		}
		st := stMsg.GetState()

		if st.GetStateOrder() <= c.lastStateOrder {
			continue
		}
		c.lastStateOrder = st.GetStateOrder()
		c.state = st

		app.QueueUpdateDraw(func() {
			tv.SetText(c.renderState(st))
		})
	}
}

// renderState — ВОЗВРАЩАЕМ СТАРУЮ ГРАФИКУ
func (c *GameClient) renderState(st *domain.GameState) string {
	if st == nil {
		return "Waiting for state..."
	}
	w := int(c.cfg.GetWidth())
	h := int(c.cfg.GetHeight())
	if w <= 0 {
		w = 40
	}
	if h <= 0 {
		h = 30
	}

	field := make([][]rune, h)
	for y := 0; y < h; y++ {
		field[y] = make([]rune, w)
		for x := 0; x < w; x++ {
			field[y][x] = ' '
		}
	}

	for _, f := range st.GetFoods() {
		x, y := int(f.GetX()), int(f.GetY())
		if x >= 0 && x < w && y >= 0 && y < h {
			field[y][x] = '*'
		}
	}

	for _, s := range st.GetSnakes() {
		cells := domain.SnakeCells(s, domain.BoardSize{Width: int32(w), Height: int32(h)})
		isMe := (s.GetPlayerId() == c.myID)
		isZombie := (s.GetState() == domain.GameState_Snake_ZOMBIE)

		for i, cell := range cells {
			cx, cy := int(cell.X), int(cell.Y)
			if cx < 0 || cx >= w || cy < 0 || cy >= h {
				continue
			}

			var char rune
			if isMe {
				if i == 0 {
					char = 'O'
				} else {
					char = 'o'
				}
			} else if isZombie {
				char = 'Z'
			} else {
				if i == 0 {
					char = 'S'
				} else {
					char = 's'
				}
			}
			field[cy][cx] = char
		}
	}

	var b strings.Builder

	// Подсчет своих очков
	myScore := 0
	for _, p := range st.GetPlayers().GetPlayers() {
		if p.GetId() == c.myID {
			myScore = int(p.GetScore())
		}
	}

	// Верхняя панель
	b.WriteString(fmt.Sprintf(
		"Игра: [white]%s[-]  |  Игрок: [yellow]%s[-]  |  Счёт: [green]%d[-]  |  Размер: %dx%d\n\n",
		c.gameName, c.playerName, myScore, w, h,
	))

	// Поле
	b.WriteString("+" + strings.Repeat("-", w) + "+\n")
	for y := 0; y < h; y++ {
		b.WriteString("|")
		for x := 0; x < w; x++ {
			r := field[y][x]
			switch r {
			case 'O', 'o':
				b.WriteString("[green]")
				b.WriteRune(r)
				b.WriteString("[-]")
			case 'S', 's':
				b.WriteString("[red]")
				b.WriteRune(r)
				b.WriteString("[-]")
			case '*':
				b.WriteString("[yellow]*[-]")
			case 'Z':
				b.WriteString("[gray]Z[-]")
			default:
				b.WriteRune(r)
			}
		}
		b.WriteString("|\n")
	}
	b.WriteString("+" + strings.Repeat("-", w) + "+\n")

	// Список игроков
	b.WriteString("\nИгроки:\n")
	for _, p := range st.GetPlayers().GetPlayers() {
		marker := ""
		if p.GetId() == c.myID {
			marker = " (YOU)"
		}
		b.WriteString(fmt.Sprintf(" - %s%s: %d\n", p.GetName(), marker, p.GetScore()))
	}

	return b.String()
}
