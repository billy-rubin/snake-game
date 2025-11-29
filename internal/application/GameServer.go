package application

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"snake-game/internal/domain"
	"snake-game/internal/infrastracture/network"
)

const (
	multicastIP   = "239.192.0.4"
	multicastPort = 9192
)

type GameServer struct {
	cfg      *domain.GameConfig
	gameName string

	engine *GameEngine
	// Последний стейт для Announcement и новых подключений
	lastState *domain.GameState

	nodeID       int32
	nextPlayerID int32
	nextMsgSeq   int64

	conn *network.UnicastConn
	log  *log.Logger

	// Защита мап игроков и времени
	mu sync.RWMutex
	// key: player_id -> адрес клиента
	players map[int32]*net.UDPAddr
	// key: player_id -> время последнего входящего пакета (для таймаута)
	playersLastSeen map[int32]time.Time
	// key: player_id -> время последнего исходящего пакета (для пинга)
	playersLastSent map[int32]time.Time

	stateUpdates <-chan *domain.GameState
	stopCh       chan struct{}
}

func NewGameServer(
	cfg *domain.GameConfig,
	gameName string,
	masterName string,
	masterType domain.PlayerType,
	logger *log.Logger,
	stateUpdates <-chan *domain.GameState,
	engine *GameEngine,
) (*GameServer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("GameConfig is nil")
	}
	if logger == nil {
		logger = log.Default()
	}

	conn, err := network.NewUnicastConn(nil)
	if err != nil {
		return nil, fmt.Errorf("create unicast conn: %w", err)
	}

	udpAddr := conn.LocalAddr()
	logger.Printf("GameServer is starting on %s", udpAddr.String())

	ip := udpAddr.IP
	if ip == nil || ip.IsUnspecified() {
		ip = net.ParseIP("127.0.0.1")
	}

	masterID := int32(1)
	masterPlayer := domain.NewPlayer(masterID, masterName, domain.NodeRole_MASTER, masterType)
	masterPlayer.IpAddress = proto.String(ip.String())
	masterPlayer.Port = proto.Int32(int32(udpAddr.Port))

	if err := engine.AddPlayer(masterPlayer); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to add master player: %w", err)
	}

	s := &GameServer{
		cfg:             cfg,
		gameName:        gameName,
		engine:          engine,
		nodeID:          masterID,
		nextPlayerID:    masterID + 1,
		nextMsgSeq:      1,
		conn:            conn,
		log:             logger,
		players:         make(map[int32]*net.UDPAddr),
		playersLastSeen: make(map[int32]time.Time),
		playersLastSent: make(map[int32]time.Time),
		stateUpdates:    stateUpdates,
		lastState:       &domain.GameState{},
		stopCh:          make(chan struct{}),
	}

	return s, nil
}

func (s *GameServer) Addr() *net.UDPAddr {
	return s.conn.LocalAddr()
}

func (s *GameServer) nextSeq() int64 {
	s.nextMsgSeq++
	return s.nextMsgSeq
}

// sendWrapper оборачивает отправку для обновления времени LastSent
func (s *GameServer) sendWrapper(msg *domain.GameMessage, to *net.UDPAddr) error {
	err := s.conn.Send(msg, to)
	if err == nil {
		// Если сообщение адресное (есть ReceiverId), обновляем таймер отправки
		if msg.ReceiverId != nil {
			recID := msg.GetReceiverId()
			s.mu.Lock()
			s.playersLastSent[recID] = time.Now()
			s.mu.Unlock()
		}
	}
	return err
}

func (s *GameServer) Run() error {
	defer s.conn.Close()

	s.log.Printf("GameServer[%s] listening on %s", s.gameName, s.conn.LocalAddr())

	go s.stateBroadcastLoop()
	go s.announceLoop()
	go s.pingerAndTimeoutLoop() // <-- Запуск проверки таймаутов

	for {
		env, err := s.conn.ReceiveOneWithTimeout(500 * time.Millisecond)
		if err != nil {
			select {
			case <-s.stopCh:
				return nil
			default:
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				s.log.Printf("receive error: %v", err)
				continue
			}
		}
		if env == nil || env.Msg == nil {
			continue
		}

		// Обновляем LastSeen при получении ЛЮБОГО сообщения
		senderID := env.Msg.GetSenderId()
		if senderID != 0 {
			s.mu.Lock()
			// Если мы знаем такого игрока, обновляем таймер
			if _, ok := s.players[senderID]; ok {
				s.playersLastSeen[senderID] = time.Now()
			}
			s.mu.Unlock()
		}

		s.handleMessage(env.Msg, env.From)
	}
}

func (s *GameServer) Stop() {
	close(s.stopCh)
}

// pingerAndTimeoutLoop отвечает за Alive Checks
func (s *GameServer) pingerAndTimeoutLoop() {
	delayMs := s.cfg.GetStateDelayMs()
	if delayMs < 100 {
		delayMs = 100
	}

	// Интервал пинга = delay / 10
	pingInterval := time.Duration(delayMs/10) * time.Millisecond
	// Таймаут = 0.8 * delay
	timeoutDuration := time.Duration(float64(delayMs)*0.8) * time.Millisecond

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.checkAlive(pingInterval, timeoutDuration)
		}
	}
}

func (s *GameServer) checkAlive(pingInterval, timeoutDuration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	// Кого удалять (нельзя удалять из map во время итерации)
	toRemove := []int32{}

	for id, addr := range s.players {
		if id == s.nodeID {
			continue // Самого себя не проверяем
		}

		// 1. Проверка таймаута (0.8 * state_delay)
		lastSeen, seenOk := s.playersLastSeen[id]
		if !seenOk {
			// Если игрока только добавили, считаем что видели только что
			s.playersLastSeen[id] = now
			lastSeen = now
		}

		if now.Sub(lastSeen) > timeoutDuration {
			s.log.Printf("Player %d timed out (last seen %v ago)", id, now.Sub(lastSeen))
			toRemove = append(toRemove, id)
			continue
		}

		// 2. Отправка Ping (если молчали > state_delay / 10)
		lastSent, sentOk := s.playersLastSent[id]
		if !sentOk {
			s.playersLastSent[id] = now
			lastSent = now
		}

		if now.Sub(lastSent) > pingInterval {
			// Шлем Ping
			pingMsg := domain.NewPingMessage(s.nextSeq(), s.nodeID, id)
			// Используем прямой conn.Send чтобы не вызвать deadlock (sendWrapper берет лок)
			// Но обновляем время вручную здесь, так как мы УЖЕ под локом
			if err := s.conn.Send(pingMsg, addr); err == nil {
				s.playersLastSent[id] = now
			}
		}
	}

	// Удаляем отвалившихся
	for _, id := range toRemove {
		delete(s.players, id)
		delete(s.playersLastSeen, id)
		delete(s.playersLastSent, id)

		// Сообщаем движку, что игрок умер (Змейка -> Zombie)
		// Делаем это в горутине, чтобы не блочить лок
		go s.engine.RemovePlayer(id)
	}
}

func (s *GameServer) stateBroadcastLoop() {
	for st := range s.stateUpdates {
		if st == nil {
			continue
		}
		s.lastState = st

		s.mu.RLock()
		if len(s.players) == 0 {
			s.mu.RUnlock()
			continue
		}

		// Копируем список адресатов под RLock
		targets := make(map[int32]*net.UDPAddr, len(s.players))
		for id, addr := range s.players {
			targets[id] = addr
		}
		s.mu.RUnlock()

		msg := domain.NewStateMessage(s.nextSeq(), s.nodeID, st)

		for id, addr := range targets {
			if addr == nil {
				continue
			}
			msg.ReceiverId = proto.Int32(id)
			// sendWrapper обновит LastSent
			if err := s.sendWrapper(msg, addr); err != nil {
				s.log.Printf("cannot send StateMsg to %s: %v", addr, err)
			}
		}
	}
}

func (s *GameServer) announceLoop() {
	group := &net.UDPAddr{
		IP:   net.ParseIP(multicastIP),
		Port: multicastPort,
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			msg := s.buildAnnouncementMsg()
			msg.ReceiverId = nil
			// Мультикаст не обновляет LastSent конкретных игроков
			s.conn.Send(msg, group)
		}
	}
}

func (s *GameServer) buildAnnouncementMsg() *domain.GameMessage {
	currentPlayers := s.lastState.GetPlayers()
	if currentPlayers == nil {
		currentPlayers = &domain.GamePlayers{}
	}

	return domain.NewAnnouncementMessage(s.nextSeq(), s.nodeID, []*domain.GameAnnouncement{
		{
			Config:   s.cfg,
			CanJoin:  proto.Bool(true),
			GameName: proto.String(s.gameName),
			Players:  currentPlayers,
		},
	})
}

func (s *GameServer) handleMessage(msg *domain.GameMessage, from *net.UDPAddr) {
	switch t := msg.Type.(type) {
	case *domain.GameMessage_Join:
		s.handleJoin(msg, t.Join, from)
	case *domain.GameMessage_Discover:
		s.handleDiscover(msg, t.Discover, from)
	case *domain.GameMessage_Steer:
		s.handleSteer(msg, t.Steer)
	case *domain.GameMessage_Ping:
		// Пинг обновляет LastSeen (это уже сделано в Run),
		// отвечать на него не нужно, он просто update alive status
	default:
	}
}

func (s *GameServer) handleJoin(
	raw *domain.GameMessage,
	join *domain.GameMessage_JoinMsg,
	from *net.UDPAddr,
) {
	if join.GetGameName() != s.gameName {
		return
	}

	newID := s.nextPlayerID
	s.nextPlayerID++

	role := join.GetRequestedRole()
	pType := join.GetPlayerType()
	name := join.GetPlayerName()

	player := domain.NewPlayer(newID, name, role, pType)
	player.IpAddress = proto.String(from.IP.String())
	player.Port = proto.Int32(int32(from.Port))

	if err := s.engine.AddPlayer(player); err != nil {
		s.log.Printf("Failed to join player %s: %v", name, err)
		errMsg := domain.NewErrorMessage(s.nextSeq(), s.nodeID, newID, err.Error())
		s.conn.Send(errMsg, from)
		return
	}

	s.mu.Lock()
	s.players[newID] = from
	s.playersLastSeen[newID] = time.Now()
	s.playersLastSent[newID] = time.Now()
	s.mu.Unlock()

	ack := domain.NewAckMessage(raw.GetMsgSeq(), s.nodeID, newID)
	if err := s.sendWrapper(ack, from); err != nil {
		s.log.Printf("cannot send Ack to %s: %v", from, err)
	}

	s.log.Printf("Player joined: %s (id=%d, role=%v)", name, newID, role)
}

func (s *GameServer) handleSteer(raw *domain.GameMessage, steer *domain.GameMessage_SteerMsg) {
	senderID := raw.GetSenderId()
	dir := steer.GetDirection()
	s.engine.ApplySteer(senderID, dir)
}

func (s *GameServer) handleDiscover(_ *domain.GameMessage, _ *domain.GameMessage_DiscoverMsg, from *net.UDPAddr) {
	if from == nil {
		return
	}
	ann := s.buildAnnouncementMsg()
	ann.ReceiverId = nil
	s.conn.Send(ann, from)
}
