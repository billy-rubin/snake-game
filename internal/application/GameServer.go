package application

import (
	"fmt"
	"log"
	"net"
	"time"

	"google.golang.org/protobuf/proto"

	"snake-game/internal/domain"
	"snake-game/internal/infrastracture/network"
)

// Константы мультикаста.
const (
	multicastIP   = "239.192.0.4"
	multicastPort = 9192
)

// GameServer обслуживает протокол MASTER-узла.
type GameServer struct {
	cfg      *domain.GameConfig
	gameName string

	// Ссылка на игровой движок (Application Logic)
	engine *GameEngine

	// Текущее состояние (кешируем для Announcement/Ack)
	lastState *domain.GameState

	nodeID       int32
	nextPlayerID int32
	nextMsgSeq   int64

	conn *network.UnicastConn
	log  *log.Logger

	// key: player_id -> адрес клиента
	players map[int32]*net.UDPAddr

	// Канал, через который движок присылает обновления
	stateUpdates <-chan *domain.GameState
}

// NewGameServer создает сервер и поднимает движок.
func NewGameServer(
	cfg *domain.GameConfig,
	gameName string,
	masterName string,
	masterType domain.PlayerType,
	logger *log.Logger,
	// channel создается снаружи в main и передается сюда и в Engine
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

	// Определяем IP для конфига
	ip := udpAddr.IP
	if ip == nil || ip.IsUnspecified() {
		ip = net.ParseIP("127.0.0.1")
	}

	// Создаем Мастера
	masterID := int32(1)
	masterPlayer := domain.NewPlayer(masterID, masterName, domain.NodeRole_MASTER, masterType)
	masterPlayer.IpAddress = proto.String(ip.String())
	masterPlayer.Port = proto.Int32(int32(udpAddr.Port))

	// Инициализируем мастера в движке
	if err := engine.AddPlayer(masterPlayer); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to add master player: %w", err)
	}

	s := &GameServer{
		cfg:          cfg,
		gameName:     gameName,
		engine:       engine,
		nodeID:       masterID,
		nextPlayerID: masterID + 1,
		nextMsgSeq:   1,
		conn:         conn,
		log:          logger,
		players:      make(map[int32]*net.UDPAddr),
		stateUpdates: stateUpdates,
		lastState:    &domain.GameState{}, // заглушка пока не придет первый стейт
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

func (s *GameServer) Run() error {
	defer s.conn.Close()

	s.log.Printf("GameServer[%s] listening on %s", s.gameName, s.conn.LocalAddr())

	// Запускаем рассылку стейтов
	go s.stateBroadcastLoop()

	// Запускаем мультикаст анонсов
	go s.announceLoop()

	// Читаем входящие сообщения
	for {
		env, err := s.conn.ReceiveOneWithTimeout(500 * time.Millisecond)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			s.log.Printf("receive error: %v", err)
			continue
		}
		if env == nil || env.Msg == nil {
			continue
		}
		s.handleMessage(env.Msg, env.From)
	}
}

func (s *GameServer) stateBroadcastLoop() {
	for st := range s.stateUpdates {
		if st == nil {
			continue
		}
		s.lastState = st

		// Если нет внешних игроков — не шлем (экономия трафика), но локально стейт обновлен
		if len(s.players) == 0 {
			continue
		}

		msg := &domain.GameMessage{
			MsgSeq:   proto.Int64(s.nextSeq()),
			SenderId: proto.Int32(s.nodeID),
			Type: &domain.GameMessage_State{
				State: &domain.GameMessage_StateMsg{
					State: st,
				},
			},
		}

		for id, addr := range s.players {
			if addr == nil {
				continue
			}
			msg.ReceiverId = proto.Int32(id)
			if err := s.conn.Send(msg, addr); err != nil {
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

	for range ticker.C {
		msg := s.buildAnnouncementMsg()
		msg.ReceiverId = nil
		if err := s.conn.Send(msg, group); err != nil {
			s.log.Printf("cannot send multicast Announcement: %v", err)
		}
	}
}

func (s *GameServer) buildAnnouncementMsg() *domain.GameMessage {
	// Берем игроков из последнего стейта
	currentPlayers := s.lastState.GetPlayers()
	if currentPlayers == nil {
		currentPlayers = &domain.GamePlayers{}
	}

	return &domain.GameMessage{
		MsgSeq:   proto.Int64(s.nextSeq()),
		SenderId: proto.Int32(s.nodeID),
		Type: &domain.GameMessage_Announcement{
			Announcement: &domain.GameMessage_AnnouncementMsg{
				Games: []*domain.GameAnnouncement{
					{
						Config:   s.cfg,
						CanJoin:  proto.Bool(true),
						GameName: proto.String(s.gameName),
						Players:  currentPlayers,
					},
				},
			},
		},
	}
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
		// TODO: обновлять время последнего контакта для таймаута
	default:
		// Ack и другие игнорируем пока
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

	// Добавляем в движок (там же происходит Spawn змеи)
	if err := s.engine.AddPlayer(player); err != nil {
		s.log.Printf("Failed to join player %s: %v", name, err)
		// Отправляем ErrorMsg
		errMsg := domain.NewErrorMessage(s.nextSeq(), s.nodeID, newID, err.Error())
		s.conn.Send(errMsg, from)
		return
	}

	// Регистрируем адрес для рассылки
	s.players[newID] = from

	// Шлем Ack с присвоенным ReceiverId
	ack := domain.NewAckMessage(raw.GetMsgSeq(), s.nodeID, newID)
	if err := s.conn.Send(ack, from); err != nil {
		s.log.Printf("cannot send Ack to %s: %v", from, err)
	}

	s.log.Printf("Player joined: %s (id=%d, role=%v)", name, newID, role)
}

func (s *GameServer) handleSteer(raw *domain.GameMessage, steer *domain.GameMessage_SteerMsg) {
	senderID := raw.GetSenderId()
	dir := steer.GetDirection()

	// Передаем управление в движок
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
