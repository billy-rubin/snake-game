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

// Константы мультикаста из задания (239.192.0.4:9192).
const (
	multicastIP   = "239.192.0.4"
	multicastPort = 9192
)

// GameServer обслуживает протокол MASTER-узла:
//   - принимает JoinMsg / DiscoverMsg
//   - рассылает Ack, Announcement
//   - по каналу stateUpdates получает GameState и шлёт StateMsg всем игрокам.
type GameServer struct {
	cfg      *domain.GameConfig
	gameName string

	state *domain.GameState

	nodeID       int32
	nextPlayerID int32
	nextMsgSeq   int64

	conn *network.UnicastConn
	log  *log.Logger

	// key: player_id -> адрес клиента (наблюдатель / игрок)
	// Мастера сюда НЕ кладём, чтобы не слать стейт самому себе.
	players map[int32]*net.UDPAddr

	// Канал от игрового движка (RunLocalSinglePlayer), через который приходят новые GameState.
	stateUpdates <-chan *domain.GameState
}

// NewGameServer поднимает UDP-сервер и регистрирует MASTER-игрока в GameState.
func NewGameServer(
	cfg *domain.GameConfig,
	gameName string,
	masterName string,
	masterType domain.PlayerType,
	logger *log.Logger,
	stateUpdates <-chan *domain.GameState,
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

	// IP, который будем анонсировать в протоколе.
	// Если LocalAddr().IP == 0.0.0.0 (unspecified), подменим на 127.0.0.1,
	// чтобы клиенты не пытались коннектиться к 0.0.0.0.
	ip := udpAddr.IP
	if ip == nil || ip.IsUnspecified() {
		ip = net.ParseIP("127.0.0.1")
	}

	masterID := int32(1)

	masterPlayer := &domain.GamePlayer{
		Id:        proto.Int32(masterID),
		Name:      proto.String(masterName),
		IpAddress: proto.String(ip.String()),
		Port:      proto.Int32(int32(udpAddr.Port)),
		Role:      domain.NodeRole_MASTER.Enum(),
		Type:      masterType.Enum(),
		Score:     proto.Int32(0),
	}

	playersList := &domain.GamePlayers{
		Players: []*domain.GamePlayer{masterPlayer},
	}

	state := &domain.GameState{
		StateOrder: proto.Int32(0),
		Players:    playersList,
		// Snakes и Foods заполняет игровой движок через stateUpdates.
	}

	s := &GameServer{
		cfg:          cfg,
		gameName:     gameName,
		state:        state,
		nodeID:       masterID,
		nextPlayerID: masterID + 1,
		nextMsgSeq:   1,
		conn:         conn,
		log:          logger,
		players:      make(map[int32]*net.UDPAddr),
		stateUpdates: stateUpdates,
	}

	return s, nil
}

// Addr возвращает адрес UDP-сервера (его надо объявлять в Announcement / Join).
func (s *GameServer) Addr() *net.UDPAddr {
	return s.conn.LocalAddr()
}

func (s *GameServer) nextSeq() int64 {
	s.nextMsgSeq++
	return s.nextMsgSeq
}

// Run запускает основной цикл обработки сообщений.
func (s *GameServer) Run() error {
	defer s.conn.Close()

	s.log.Printf("GameServer[%s] listening on %s", s.gameName, s.conn.LocalAddr())

	// Рассылка StateMsg.
	if s.stateUpdates != nil {
		go s.stateBroadcastLoop()
	}

	// Периодическая рассылка Announcement по мультикасту,
	// чтобы лобби видело открытое лобби мастера.
	go s.announceLoop()

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

// stateBroadcastLoop — рассылка StateMsg всем подключенным игрокам/наблюдателям.
func (s *GameServer) stateBroadcastLoop() {
	for st := range s.stateUpdates {
		if st == nil {
			continue
		}

		// Обновляем текущее состояние (для последующих Announcement).
		s.state = st

		// Если пока нет ни одного удалённого клиента, смысла слать стейт нет.
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
			// Подстрахуемся от 0.0.0.0, чтобы не ловить ошибки как в логах.
			if addr.IP == nil || addr.IP.IsUnspecified() {
				s.log.Printf("skip sending StateMsg to unspecified IP for player %d: %v", id, addr)
				continue
			}

			msg.ReceiverId = proto.Int32(id)

			if err := s.conn.Send(msg, addr); err != nil {
				s.log.Printf("cannot send StateMsg to %s: %v", addr, err)
			}
		}
	}
}

// announceLoop — периодически шлём AnnouncementMsg в мультикаст,
// чтобы все лобби (Menu/Lobby) могли увидеть эту игру в списке.
func (s *GameServer) announceLoop() {
	group := &net.UDPAddr{
		IP:   net.ParseIP(multicastIP),
		Port: multicastPort,
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		msg := s.buildAnnouncementMsg()
		// Для мультикаста ReceiverId обычно не заполняется.
		msg.ReceiverId = nil

		if err := s.conn.Send(msg, group); err != nil {
			s.log.Printf("cannot send multicast Announcement to %s: %v", group, err)
		}
	}
}

// buildAnnouncementMsg собирает AnnouncementMsg с одной игрой (текущий сервер).
func (s *GameServer) buildAnnouncementMsg() *domain.GameMessage {
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
						Players:  s.state.GetPlayers(),
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
	default:
		s.log.Printf("ignore message type %T from %s", t, from)
	}
}

// Обработка JoinMsg: регистрируем игрока, отправляем Ack и Announcement (уже по unicast).
func (s *GameServer) handleJoin(
	raw *domain.GameMessage,
	join *domain.GameMessage_JoinMsg,
	from *net.UDPAddr,
) {
	if join == nil {
		s.log.Printf("nil JoinMsg from %s", from)
		return
	}
	if join.GetGameName() != s.gameName {
		s.log.Printf("JOIN for unknown game %q from %s", join.GetGameName(), from)
		return
	}

	id := s.nextPlayerID
	s.nextPlayerID++

	player := &domain.GamePlayer{
		Id:        proto.Int32(id),
		Name:      proto.String(join.GetPlayerName()),
		IpAddress: proto.String(from.IP.String()),
		Port:      proto.Int32(int32(from.Port)),
		Role:      join.GetRequestedRole().Enum(),
		Type:      join.GetPlayerType().Enum(),
		Score:     proto.Int32(0),
	}

	if s.state.GetPlayers() == nil {
		s.state.Players = &domain.GamePlayers{}
	}
	s.state.Players.Players = append(s.state.Players.Players, player)
	s.players[id] = from

	// Ack: msg_seq совпадает с Join.
	ack := &domain.GameMessage{
		MsgSeq:     proto.Int64(raw.GetMsgSeq()),
		SenderId:   proto.Int32(s.nodeID),
		ReceiverId: proto.Int32(id),
		Type: &domain.GameMessage_Ack{
			Ack: &domain.GameMessage_AckMsg{},
		},
	}

	if err := s.conn.Send(ack, from); err != nil {
		s.log.Printf("cannot send Ack to %s: %v", from, err)
	}

	// Одноразовый Announcement только подключившемуся игроку.
	ann := s.buildAnnouncementMsg()
	ann.ReceiverId = proto.Int32(id)

	if err := s.conn.Send(ann, from); err != nil {
		s.log.Printf("cannot send Announcement to %s: %v", from, err)
	}

	s.log.Printf("JOIN ok: player=%q id=%d role=%v from=%s",
		join.GetPlayerName(), id, join.GetRequestedRole(), from)
}

// handleDiscover — ответ на DiscoverMsg: шлём Announcement только инициатору.
func (s *GameServer) handleDiscover(
	_ *domain.GameMessage,
	_ *domain.GameMessage_DiscoverMsg,
	from *net.UDPAddr,
) {
	if from == nil {
		return
	}

	ann := s.buildAnnouncementMsg()
	// Unicast, можно не указывать ReceiverId или оставить nil.
	ann.ReceiverId = nil

	if err := s.conn.Send(ann, from); err != nil {
		s.log.Printf("cannot send Announcement (discover response) to %s: %v", from, err)
	}
}
