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

	// Alive checks
	mu             sync.Mutex
	serverLastSeen time.Time // Когда последний раз получали пакет от сервера
	lastSent       time.Time // Когда последний раз что-то слали

	stopCh chan struct{}
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
		stopCh:     make(chan struct{}),
	}, nil
}

func (c *GameClient) incrementNextSeq() int64 {
	c.nextSeq++
	return c.nextSeq
}

// sendWrapper оборачивает отправку для обновления lastSent
func (c *GameClient) sendWrapper(msg *domain.GameMessage) error {
	err := c.conn.Send(msg, c.serverAddr)
	if err == nil {
		c.mu.Lock()
		c.lastSent = time.Now()
		c.mu.Unlock()
	}
	return err
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

	if err := c.sendWrapper(joinMsg); err != nil {
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

		// Обновляем время получения
		c.mu.Lock()
		c.serverLastSeen = time.Now()
		c.mu.Unlock()

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

	// Инициализируем таймеры после успешного Join
	c.mu.Lock()
	now := time.Now()
	c.serverLastSeen = now
	c.lastSent = now
	c.mu.Unlock()

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

	var wg sync.WaitGroup
	wg.Add(2)

	// Читатель
	go func() {
		defer wg.Done()
		c.recvLoop(app, textView)
	}()

	// Пингер / Чекера таймаута
	go func() {
		defer wg.Done()
		c.pingerLoop(app)
	}()

	err := app.Run()
	close(c.stopCh)
	wg.Wait()
	_ = c.conn.Close()

	return err
}

func (c *GameClient) sendSteer(d domain.Direction) {
	msg := domain.NewSteerMessage(c.incrementNextSeq(), c.myID, d)
	if err := c.sendWrapper(msg); err != nil {
		c.log.Printf("failed to send steer: %v", err)
	}
}

// pingerLoop следит за связью с сервером
func (c *GameClient) pingerLoop(app *tview.Application) {
	delayMs := c.cfg.GetStateDelayMs()
	if delayMs < 100 {
		delayMs = 100
	}

	pingInterval := time.Duration(delayMs/10) * time.Millisecond
	timeoutDuration := time.Duration(float64(delayMs)*0.8) * time.Millisecond

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.mu.Lock()
			lastSeen := c.serverLastSeen
			lastSent := c.lastSent
			c.mu.Unlock()

			now := time.Now()

			// 1. Проверка: не умер ли сервер?
			if now.Sub(lastSeen) > timeoutDuration {
				c.log.Printf("Server timed out! (last seen %v ago)", now.Sub(lastSeen))
				app.Stop() // Выход из UI
				return
			}

			// 2. Отправка пинга, если мы молчим
			if now.Sub(lastSent) > pingInterval {
				pingMsg := domain.NewPingMessage(c.incrementNextSeq(), c.myID, 0)
				// sendWrapper возьмет лок и обновит lastSent
				if err := c.sendWrapper(pingMsg); err != nil {
					c.log.Printf("failed to send Ping: %v", err)
				}
			}
		}
	}
}

func (c *GameClient) recvLoop(app *tview.Application, tv *tview.TextView) {
	for {
		select {
		case <-c.stopCh:
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

		// Обновляем таймер, что сервер жив
		c.mu.Lock()
		c.serverLastSeen = time.Now()
		c.mu.Unlock()

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

	myScore := 0
	for _, p := range st.GetPlayers().GetPlayers() {
		if p.GetId() == c.myID {
			myScore = int(p.GetScore())
		}
	}

	b.WriteString(fmt.Sprintf(
		"Игра: [white]%s[-]  |  Игрок: [yellow]%s[-]  |  Счёт: [green]%d[-]  |  Размер: %dx%d\n\n",
		c.gameName, c.playerName, myScore, w, h,
	))

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
