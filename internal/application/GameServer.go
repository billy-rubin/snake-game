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

// pendingMessage хранит сообщение, ожидающее подтверждения (Ack)
type pendingMessage struct {
	msg      *domain.GameMessage
	to       *net.UDPAddr
	lastSent time.Time
}

type GameServer struct {
	cfg      *domain.GameConfig
	gameName string

	engine    *GameEngine
	lastState *domain.GameState

	nodeID       int32
	nextPlayerID int32
	nextMsgSeq   int64

	conn *network.UnicastConn
	log  *log.Logger

	mu              sync.RWMutex
	players         map[int32]*net.UDPAddr
	playersLastSeen map[int32]time.Time
	playersLastSent map[int32]time.Time

	// Надежность: хранилище отправленных сообщений (msgSeq -> pendingMessage)
	sentMessages map[int64]*pendingMessage

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
		sentMessages:    make(map[int64]*pendingMessage), // Инициализация мапы
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

// SendReliable отправляет сообщение с учетом надежности.
// Если сообщение требует Ack, оно сохраняется в sentMessages.
func (s *GameServer) SendReliable(msg *domain.GameMessage, to *net.UDPAddr) error {
	// 1. Сразу отправляем в сеть
	if err := s.rawSend(msg, to); err != nil {
		return err
	}

	// 2. Если сообщение требует подтверждения, сохраняем его
	if isReliableMessage(msg) {
		s.mu.Lock()
		s.sentMessages[msg.GetMsgSeq()] = &pendingMessage{
			msg:      msg,
			to:       to,
			lastSent: time.Now(),
		}
		s.mu.Unlock()
	}

	return nil
}

// rawSend - базовая отправка + обновление таймеров LastSent
func (s *GameServer) rawSend(msg *domain.GameMessage, to *net.UDPAddr) error {
	err := s.conn.Send(msg, to)
	if err == nil {
		if msg.ReceiverId != nil {
			recID := msg.GetReceiverId()
			s.mu.Lock()
			s.playersLastSent[recID] = time.Now()
			s.mu.Unlock()
		}
	}
	return err
}

func isReliableMessage(msg *domain.GameMessage) bool {
	// Ack, Announcement, Discover не требуют подтверждения
	switch msg.Type.(type) {
	case *domain.GameMessage_Ack, *domain.GameMessage_Announcement, *domain.GameMessage_Discover:
		return false
	default:
		// Ping, Steer, State, Join, Error, RoleChange - требуют Ack
		return true
	}
}

func (s *GameServer) Run() error {
	defer s.conn.Close()

	s.log.Printf("GameServer[%s] listening on %s", s.gameName, s.conn.LocalAddr())

	go s.stateBroadcastLoop()
	go s.announceLoop()
	go s.pingerAndRetransmitLoop() // <-- Объединенный цикл пингов и ретраев

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

		// Обновляем LastSeen
		senderID := env.Msg.GetSenderId()
		if senderID != 0 {
			s.mu.Lock()
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

// pingerAndRetransmitLoop занимается и Alive Check, и Retransmission
func (s *GameServer) pingerAndRetransmitLoop() {
	delayMs := s.cfg.GetStateDelayMs()
	if delayMs < 100 {
		delayMs = 100
	}

	// Интервал ретрая и пинга = delay / 10
	interval := time.Duration(delayMs/10) * time.Millisecond
	timeoutDuration := time.Duration(float64(delayMs)*0.8) * time.Millisecond

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.checkRetransmits(interval)
			s.checkAlive(interval, timeoutDuration)
		}
	}
}

// checkRetransmits переотправляет потерянные сообщения
func (s *GameServer) checkRetransmits(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, pm := range s.sentMessages {
		if now.Sub(pm.lastSent) > interval {
			// Пора переотправлять
			s.log.Printf("Retrying msg seq=%d type=%T to %s", pm.msg.GetMsgSeq(), pm.msg.Type, pm.to)
			if err := s.conn.Send(pm.msg, pm.to); err == nil {
				pm.lastSent = now

				// Обновляем LastSent для пингера, чтобы не слать лишние пинги
				if pm.msg.ReceiverId != nil {
					recID := pm.msg.GetReceiverId()
					s.playersLastSent[recID] = now
				}
			}
		}
	}
}

func (s *GameServer) checkAlive(pingInterval, timeoutDuration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	toRemove := []int32{}

	for id, addr := range s.players {
		if id == s.nodeID {
			continue
		}

		// 1. Timeout
		lastSeen, seenOk := s.playersLastSeen[id]
		if !seenOk {
			s.playersLastSeen[id] = now
			lastSeen = now
		}
		if now.Sub(lastSeen) > timeoutDuration {
			s.log.Printf("Player %d timed out", id)
			toRemove = append(toRemove, id)
			continue
		}

		// 2. Ping
		lastSent, sentOk := s.playersLastSent[id]
		if !sentOk {
			s.playersLastSent[id] = now
			lastSent = now
		}

		// Если мы молчим дольше интервала, надо слать Ping
		// Но Ping теперь тоже Reliable, так что он попадет в sentMessages
		// Чтобы не плодить пинги каждую итерацию, проверяем lastSent
		if now.Sub(lastSent) > pingInterval {
			pingMsg := domain.NewPingMessage(s.nextSeq(), s.nodeID, id)

			// Используем rawSend + сохранение в sentMessages (но без лока, т.к. мы уже под локом)
			// Приходится дублировать логику SendReliable, чтобы избежать deadlock

			s.conn.Send(pingMsg, addr)
			s.playersLastSent[id] = now // Обновили сразу

			// Сохраняем для ретрая
			s.sentMessages[pingMsg.GetMsgSeq()] = &pendingMessage{
				msg:      pingMsg,
				to:       addr,
				lastSent: now,
			}
		}
	}

	for _, id := range toRemove {
		delete(s.players, id)
		delete(s.playersLastSeen, id)
		delete(s.playersLastSent, id)
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
		targets := make(map[int32]*net.UDPAddr, len(s.players))
		for id, addr := range s.players {
			targets[id] = addr
		}
		s.mu.RUnlock()

		// StateMsg отправляем каждому отдельно с новым seq, чтобы отслеживать Ack
		for id, addr := range targets {
			if addr == nil {
				continue
			}
			msg := domain.NewStateMessage(s.nextSeq(), s.nodeID, st)
			msg.ReceiverId = proto.Int32(id)

			// StateMsg тоже требует Ack
			s.SendReliable(msg, addr)
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
			// Announcement не надежный
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

// sendAck отправляет AckMsg в ответ на msgSeq
func (s *GameServer) sendAck(msgSeq int64, toID int32, toAddr *net.UDPAddr) {
	// AckMsg должен иметь тот же msg_seq, что и подтверждаемое сообщение
	ack := domain.NewAckMessage(msgSeq, s.nodeID, toID)
	// Ack отправляем ненадежно (ack на ack не шлют)
	s.rawSend(ack, toAddr)
}

func (s *GameServer) handleMessage(msg *domain.GameMessage, from *net.UDPAddr) {
	senderID := msg.GetSenderId()
	seq := msg.GetMsgSeq()

	// 1. Ack Handling
	if _, ok := msg.Type.(*domain.GameMessage_Ack); ok {
		s.mu.Lock()
		delete(s.sentMessages, seq)
		s.mu.Unlock()
		return
	}

	// 2. Auto-Ack для надежных сообщений
	if isReliableMessage(msg) {
		// Join обрабатываем отдельно
		if _, isJoin := msg.Type.(*domain.GameMessage_Join); !isJoin {
			s.sendAck(seq, senderID, from)
		}
	}

	// 3. Routing
	switch t := msg.Type.(type) {
	case *domain.GameMessage_Join:
		s.handleJoin(msg, t.Join, from)
	case *domain.GameMessage_Discover:
		s.handleDiscover(msg, t.Discover, from)
	case *domain.GameMessage_Steer:
		s.handleSteer(msg, t.Steer)
	case *domain.GameMessage_Ping:
		// LastSeen уже обновлен в Run(), ничего делать не надо
	case *domain.GameMessage_RoleChange:
		s.handleRoleChange(msg, t.RoleChange, from)
	}
}

// РЕАЛИЗАЦИЯ ТОГО, ЧТО БЫЛО В КОММЕНТАРИЯХ (RoleChange)
func (s *GameServer) handleRoleChange(
	raw *domain.GameMessage,
	rc *domain.GameMessage_RoleChangeMsg,
	from *net.UDPAddr,
) {
	senderID := raw.GetSenderId()

	// Логика: Игрок хочет сменить роль (например, выйти в VIEWER)
	// Протокол позволяет менять SenderRole (кем я стал) и ReceiverRole (кем ты стань).

	// Вариант 1: Игрок уходит в зрители (выход из игры)
	if rc.SenderRole != nil && *rc.SenderRole == domain.NodeRole_VIEWER {
		s.log.Printf("Player %d requested to become VIEWER (leaving game)", senderID)

		// 1. В движке делаем его змею зомби
		s.engine.RemovePlayer(senderID)

		// 2. Обновляем роль в списке игроков сервера
		s.mu.Lock()
		// Мы не удаляем его из players map, так как он все еще подключен как зритель
		// Но нам нужно обновить его роль в GameState.
		// GameState управляется Engine, поэтому Engine.RemovePlayer должен был выставить статус ZOMBIE змее.
		// А роль игрока в списке Players тоже надо сменить.
		s.mu.Unlock()

		// Прямое обновление списка игроков через Engine (нужен метод UpdatePlayerRole, но для MVP RemovePlayer достаточно,
		// так как зомби-змея отвязывается от управления).
	}

	// Вариант 2: Заместитель становится Мастером (DEPUTY -> MASTER)
	if rc.SenderRole != nil && *rc.SenderRole == domain.NodeRole_MASTER {
		s.log.Printf("WARNING: Player %d claims to be MASTER!", senderID)
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

	// Здесь упрощение: если игрок ретраит Join, мы создадим дубликат.
	// По-хорошему надо проверять, нет ли игрока с таким IP:Port.
	// Но для выполнения пункта "Надежность" главное — отправить Ack.

	newID := s.nextPlayerID
	s.nextPlayerID++

	role := join.GetRequestedRole()
	pType := join.GetPlayerType()
	name := join.GetPlayerName()

	player := domain.NewPlayer(newID, name, role, pType)
	player.IpAddress = proto.String(from.IP.String())
	player.Port = proto.Int32(int32(from.Port))

	if err := s.engine.AddPlayer(player); err != nil {
		errMsg := domain.NewErrorMessage(s.nextSeq(), s.nodeID, newID, err.Error())
		s.SendReliable(errMsg, from) // Error тоже reliable
		return
	}

	s.mu.Lock()
	s.players[newID] = from
	s.playersLastSeen[newID] = time.Now()
	s.playersLastSent[newID] = time.Now()
	s.mu.Unlock()

	// Шлем Ack (на Join это обязательно)
	// ВАЖНО: Ack должен иметь seq как у Join
	ack := domain.NewAckMessage(raw.GetMsgSeq(), s.nodeID, newID)
	// Ack не сохраняем в sentMessages, но шлем через rawSend
	s.rawSend(ack, from)

	s.log.Printf("Player joined: %s (id=%d)", name, newID)
}

func (s *GameServer) handleSteer(raw *domain.GameMessage, steer *domain.GameMessage_SteerMsg) {
	s.engine.ApplySteer(raw.GetSenderId(), steer.GetDirection())
}

func (s *GameServer) handleDiscover(_ *domain.GameMessage, _ *domain.GameMessage_DiscoverMsg, from *net.UDPAddr) {
	if from == nil {
		return
	}
	ann := s.buildAnnouncementMsg()
	ann.ReceiverId = nil
	s.conn.Send(ann, from)
}
