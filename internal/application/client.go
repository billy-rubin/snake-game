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
	"google.golang.org/protobuf/proto"

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
		nodeID:     100, // произвольный client node_id
		nextSeq:    1,
	}, nil
}

func (c *GameClient) incrementNextSeq() int64 {
	c.nextSeq++
	return c.nextSeq
}

// JoinOnce выполняет один цикл Join + ожидание Ack (+ попытка получить Announcement).
// ВАЖНО: соединение НЕ закрывается здесь — оно нужно для дальнейшего приёма StateMsg.
func (c *GameClient) JoinOnce() error {
	seq := c.incrementNextSeq()

	join := &domain.GameMessage{
		MsgSeq:   proto.Int64(seq),
		SenderId: proto.Int32(c.nodeID),
		Type: &domain.GameMessage_Join{
			Join: &domain.GameMessage_JoinMsg{
				GameName:      proto.String(c.gameName),
				PlayerName:    proto.String(c.playerName),
				PlayerType:    c.playerType.Enum(),
				RequestedRole: domain.NodeRole_VIEWER.Enum(), // сейчас всегда наблюдатель
			},
		},
	}

	if err := c.conn.Send(join, c.serverAddr); err != nil {
		return fmt.Errorf("send Join: %w", err)
	}
	c.log.Printf("JOIN sent seq=%d to %s", seq, c.serverAddr)

	gotAck := false

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		env, err := c.conn.ReceiveOneWithTimeout(500 * time.Millisecond)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			c.log.Printf("receive after Join error: %v", err)
			continue
		}
		if env == nil || env.Msg == nil {
			continue
		}

		msg := env.Msg

		switch t := msg.Type.(type) {
		case *domain.GameMessage_Ack:
			_ = t
			gotAck = true
			c.log.Printf("ACK received from %s for seq=%d", env.From, msg.GetMsgSeq())

		case *domain.GameMessage_Announcement:
			ann := t.Announcement
			if ann == nil {
				continue
			}
			for _, g := range ann.GetGames() {
				if g.GetGameName() == c.gameName {
					c.cfg = g.GetConfig()
					c.log.Printf("ANN for game %q: %dx%d players=%d",
						c.gameName,
						c.cfg.GetWidth(), c.cfg.GetHeight(),
						len(g.GetPlayers().GetPlayers()),
					)
				}
			}

		default:
			// другие типы игнорируем
		}

		if gotAck {
			break
		}
	}

	if !gotAck {
		return fmt.Errorf("no Ack for Join")
	}

	if c.cfg == nil {
		// На всякий случай, чтобы рендер не упал.
		c.cfg = &domain.GameConfig{}
	}

	return nil
}

// RunViewer — tview-приложение, которое в реальном времени показывает приходящие StateMsg.
func (c *GameClient) RunViewer() error {
	app := tview.NewApplication()

	text := tview.NewTextView().
		SetDynamicColors(true).
		SetChangedFunc(func() {
			app.Draw()
		})
	text.SetBorder(true).
		SetTitle(fmt.Sprintf("Game: %s  (viewer: %s)", c.gameName, c.playerName))

	// Управление: q / ESC / Ctrl+C — выход.
	text.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyESC, tcell.KeyCtrlC:
			app.Stop()
			return nil
		}
		switch event.Rune() {
		case 'q', 'Q':
			app.Stop()
			return nil
		}
		return event
	})

	app.SetRoot(text, true)

	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		c.recvStateLoop(stopCh, app, text)
	}()

	err := app.Run()

	close(stopCh)
	wg.Wait()
	_ = c.conn.Close()

	return err
}

// recvStateLoop слушает StateMsg и обновляет текстовое представление поля.
func (c *GameClient) recvStateLoop(stopCh <-chan struct{}, app *tview.Application, text *tview.TextView) {
	for {
		select {
		case <-stopCh:
			return
		default:
		}

		env, err := c.conn.ReceiveOneWithTimeout(500 * time.Millisecond)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			c.log.Printf("viewer receive error: %v", err)
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
			text.SetText(renderRemoteState(c.cfg, c.state))
		})
	}
}

// Очень упрощённый рендер: рисуем поле, еду и только головы змей.
func renderRemoteState(cfg *domain.GameConfig, st *domain.GameState) string {
	if st == nil {
		return "Waiting for game state...\n\nPress 'q' or ESC to exit viewer."
	}

	w := int(cfg.GetWidth())
	h := int(cfg.GetHeight())
	if w <= 0 {
		w = 40
	}
	if h <= 0 {
		h = 25
	}

	board := make([][]rune, h)
	for y := 0; y < h; y++ {
		row := make([]rune, w)
		for x := 0; x < w; x++ {
			row[x] = ' '
		}
		board[y] = row
	}

	// Еда
	for _, f := range st.GetFoods() {
		x := int(f.GetX())
		y := int(f.GetY())
		if x >= 0 && x < w && y >= 0 && y < h {
			board[y][x] = '*'
		}
	}

	// Головы змей
	for _, s := range st.GetSnakes() {
		points := s.GetPoints()
		if len(points) == 0 {
			continue
		}
		head := points[0]
		x := int(head.GetX())
		y := int(head.GetY())
		if x >= 0 && x < w && y >= 0 && y < h {
			board[y][x] = 'S'
		}
	}

	var b strings.Builder

	for y := 0; y < h; y++ {
		b.WriteString("|")
		for x := 0; x < w; x++ {
			b.WriteRune(board[y][x])
		}
		b.WriteString("|\n")
	}

	b.WriteString("\nPlayers:\n")
	if st.GetPlayers() != nil {
		for _, p := range st.GetPlayers().GetPlayers() {
			fmt.Fprintf(&b, "- %s (score=%d, role=%s)\n",
				p.GetName(), p.GetScore(), p.GetRole().String())
		}
	}

	b.WriteString("\nPress 'q' or ESC to exit viewer.")
	return b.String()
}
