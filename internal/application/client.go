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

// Структура для очереди клиента
type clientPendingMsg struct {
	msg      *domain.GameMessage
	lastSent time.Time
}

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

	mu             sync.Mutex
	serverLastSeen time.Time
	lastSent       time.Time

	sentMessages map[int64]*clientPendingMsg

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
		conn:         conn,
		serverAddr:   serverAddr,
		log:          logger,
		gameName:     gameName,
		playerName:   playerName,
		playerType:   pType,
		reqRole:      role,
		nodeID:       0,
		nextSeq:      1,
		sentMessages: make(map[int64]*clientPendingMsg),
		stopCh:       make(chan struct{}),
	}, nil
}

func (c *GameClient) incrementNextSeq() int64 {
	c.nextSeq++
	return c.nextSeq
}

func isReliableMessageC(msg *domain.GameMessage) bool {
	switch msg.Type.(type) {
	case *domain.GameMessage_Ack, *domain.GameMessage_Announcement, *domain.GameMessage_Discover:
		return false
	default:
		return true
	}
}

func (c *GameClient) SendReliable(msg *domain.GameMessage) error {
	if err := c.rawSend(msg); err != nil {
		return err
	}

	if isReliableMessageC(msg) {
		c.mu.Lock()
		c.sentMessages[msg.GetMsgSeq()] = &clientPendingMsg{
			msg:      msg,
			lastSent: time.Now(),
		}
		c.mu.Unlock()
	}
	return nil
}

func (c *GameClient) rawSend(msg *domain.GameMessage) error {
	err := c.conn.Send(msg, c.serverAddr)
	if err == nil {
		c.mu.Lock()
		c.lastSent = time.Now()
		c.mu.Unlock()
	}
	return err
}

func (c *GameClient) sendAck(msgSeq int64) {
	ack := domain.NewAckMessage(msgSeq, c.nodeID, 0)
	c.rawSend(ack)
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

	// Join теперь тоже reliable
	if err := c.SendReliable(joinMsg); err != nil {
		return fmt.Errorf("send Join: %w", err)
	}
	c.log.Printf("JOIN sent seq=%d to %s", seq, c.serverAddr)

	gotAck := false
	deadline := time.Now().Add(3 * time.Second)

	ticker := time.NewTicker(time.Millisecond * 100)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		c.mu.Lock()
		if pm, ok := c.sentMessages[seq]; ok {
			if time.Since(pm.lastSent) > 1000*time.Millisecond {
				c.conn.Send(pm.msg, c.serverAddr)
				pm.lastSent = time.Now()
			}
		}
		c.mu.Unlock()

		env, err := c.conn.ReceiveOneWithTimeout(100 * time.Millisecond)
		if err != nil {
			continue
		}
		if env == nil || env.Msg == nil {
			continue
		}

		msg := env.Msg
		if _, isAck := msg.Type.(*domain.GameMessage_Ack); isAck {
			if msg.GetMsgSeq() == seq { // Подтверждение нашего Join
				if msg.ReceiverId != nil {
					c.myID = msg.GetReceiverId()
					c.nodeID = c.myID
				}
				gotAck = true

				c.mu.Lock()
				delete(c.sentMessages, seq)
				c.mu.Unlock()

				c.log.Printf("ACK received. My PlayerID = %d", c.myID)
			}
		} else if t, isErr := msg.Type.(*domain.GameMessage_Error); isErr {
			return fmt.Errorf("server rejected join: %s", t.Error.GetErrorMessage())
		} else if t, isAnn := msg.Type.(*domain.GameMessage_Announcement); isAnn {
			// Ловим конфиг
			if t.Announcement != nil {
				for _, g := range t.Announcement.GetGames() {
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

	c.mu.Lock()
	now := time.Now()
	c.serverLastSeen = now
	c.lastSent = now
	c.mu.Unlock()

	return nil
}

func (c *GameClient) LeaveGame() {
	if c.reqRole == domain.NodeRole_VIEWER {
		return
	}

	viewer := domain.NodeRole_VIEWER
	msg := domain.NewRoleChangeMessage(
		c.incrementNextSeq(),
		c.myID,
		0, // Серверу
		&viewer,
		nil,
	)

	c.log.Printf("Sending LeaveGame (RoleChange -> VIEWER)...")
	c.conn.Send(msg, c.serverAddr)
	time.Sleep(50 * time.Millisecond)
	c.conn.Send(msg, c.serverAddr)
}

func (c *GameClient) RunGame() error {
	app := tview.NewApplication()

	textView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false).
		SetChangedFunc(func() { app.Draw() })

	textView.SetBorder(true).SetTitle(fmt.Sprintf(" Game: %s ", c.gameName))

	textView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' || event.Rune() == 'Q' {
			go c.LeaveGame()

			time.Sleep(100 * time.Millisecond)
			app.Stop()
			return nil
		}

		if c.reqRole == domain.NodeRole_VIEWER || c.playerType == domain.PlayerType_ROBOT {
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

	go func() {
		defer wg.Done()
		c.recvLoop(app, textView)
	}()

	go func() {
		defer wg.Done()
		c.pingerAndRetransmitLoop(app)
	}()

	err := app.Run()
	close(c.stopCh)
	wg.Wait()
	_ = c.conn.Close()

	return err
}

func (c *GameClient) sendSteer(d domain.Direction) {
	msg := domain.NewSteerMessage(c.incrementNextSeq(), c.myID, d)
	// Steer теперь Reliable
	if err := c.SendReliable(msg); err != nil {
		c.log.Printf("failed to send steer: %v", err)
	}
}

func (c *GameClient) pingerAndRetransmitLoop(app *tview.Application) {
	delayMs := c.cfg.GetStateDelayMs()
	if delayMs < 100 {
		delayMs = 100
	}
	interval := time.Duration(delayMs/10) * time.Millisecond
	timeoutDuration := time.Duration(float64(delayMs)*0.8) * time.Millisecond

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.mu.Lock()
			// Ретраи
			now := time.Now()
			for _, pm := range c.sentMessages {
				if now.Sub(pm.lastSent) > interval {
					c.conn.Send(pm.msg, c.serverAddr)
					pm.lastSent = now
					c.lastSent = now
				}
			}

			// Таймаут
			if now.Sub(c.serverLastSeen) > timeoutDuration {
				c.mu.Unlock()
				c.log.Printf("Server timed out!")
				app.Stop()
				return
			}

			// Пинг
			if now.Sub(c.lastSent) > interval {

				pingMsg := domain.NewPingMessage(c.incrementNextSeq(), c.myID, 0)
				c.conn.Send(pingMsg, c.serverAddr)

				c.lastSent = now
				c.sentMessages[pingMsg.GetMsgSeq()] = &clientPendingMsg{
					msg:      pingMsg,
					lastSent: now,
				}
			}
			c.mu.Unlock()
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

		c.mu.Lock()
		c.serverLastSeen = time.Now()
		c.mu.Unlock()

		msg := env.Msg
		seq := msg.GetMsgSeq()

		// Обработка Ack
		if _, isAck := msg.Type.(*domain.GameMessage_Ack); isAck {
			c.mu.Lock()
			delete(c.sentMessages, seq)
			c.mu.Unlock()
			continue
		}

		// Если сообщение требует Ack, шлем его
		if isReliableMessageC(msg) {
			c.sendAck(seq)
		}

		// Обработка State
		if stMsg := msg.GetState(); stMsg != nil {
			st := stMsg.GetState()
			if st.GetStateOrder() > c.lastStateOrder {
				c.lastStateOrder = st.GetStateOrder()
				c.state = st
				app.QueueUpdateDraw(func() {
					tv.SetText(c.renderState(st))
				})
			}
		}
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
