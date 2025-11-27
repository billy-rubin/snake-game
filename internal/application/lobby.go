package application

import (
	"fmt"
	"net"
	"sync"
	"time"

	"snake-game/internal/domain"
	"snake-game/internal/infrastracture/network"
)

// универсальный интерфейс логгера
type Logger interface {
	Printf(format string, v ...any)
}

// пустой логгер, если nil
type nilLogger struct{}

func (nilLogger) Printf(string, ...any) {}

// Lobby занимается discovery игр через multicast.
type Lobby struct {
	log      Logger
	onUpdate func([]*domain.GameAnnouncement)

	conn  *network.UnicastConn       // для отправки Discover
	mcast *network.MulticastListener // для приёма Announcement

	mu    sync.Mutex
	games map[string]*domain.GameAnnouncement // key: gameName@ip:port

	stopCh chan struct{}
	wg     sync.WaitGroup

	nodeID  int32
	nextSeq int64
}

func NewLobby(log Logger, onUpdate func([]*domain.GameAnnouncement)) (*Lobby, error) {
	if log == nil {
		log = nilLogger{}
	}
	if onUpdate == nil {
		return nil, fmt.Errorf("onUpdate callback is required")
	}

	conn, err := network.NewUnicastConn(nil)
	if err != nil {
		return nil, fmt.Errorf("create discovery unicast conn: %w", err)
	}
	mcast, err := network.NewMulticastListener()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("create multicast listener: %w", err)
	}

	l := &Lobby{
		log:      log,
		onUpdate: onUpdate,
		conn:     conn,
		mcast:    mcast,
		games:    make(map[string]*domain.GameAnnouncement),
		stopCh:   make(chan struct{}),
		nodeID:   500,
	}

	l.log.Printf("Lobby started. local UDP addr=%s multicast=%s",
		l.conn.LocalAddr(), network.MulticastAddress)

	l.wg.Add(2)
	go l.senderLoop()
	go l.receiverLoop()

	return l, nil
}

func (l *Lobby) Close() {
	close(l.stopCh)
	if l.mcast != nil {
		_ = l.mcast.Close()
	}
	if l.conn != nil {
		_ = l.conn.Close()
	}
	l.wg.Wait()
}

func (l *Lobby) incrementNextSeq() int64 {
	l.nextSeq++
	return l.nextSeq
}

func (l *Lobby) senderLoop() {
	defer l.wg.Done()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			seq := l.incrementNextSeq()
			msg := domain.NewDiscoverMessage(seq, l.nodeID)
			if err := l.conn.SendMulticast(msg); err != nil {
				l.log.Printf("Lobby: send Discover error: %v", err)
			}
		}
	}
}

func (l *Lobby) receiverLoop() {
	defer l.wg.Done()

	for {
		select {
		case <-l.stopCh:
			return
		default:
		}

		env, err := l.mcast.ReceiveOne()
		if err != nil {
			return
		}
		if env == nil || env.Msg == nil {
			continue
		}

		switch t := env.Msg.Type.(type) {
		case *domain.GameMessage_Announcement:
			l.handleAnnouncement(t.Announcement, env.From)
		}
	}
}

func (l *Lobby) handleAnnouncement(ann *domain.GameMessage_AnnouncementMsg, from *net.UDPAddr) {
	if ann == nil {
		return
	}

	changed := false
	l.mu.Lock()
	defer func() {
		l.mu.Unlock()
		if changed {
			l.emitUpdate()
		}
	}()

	for _, g := range ann.GetGames() {
		gameName := g.GetGameName()
		masterIP, masterPort := extractMasterAddr(g)
		if masterIP == "" {
			masterIP = from.IP.String()
		}
		if masterPort == 0 {
			masterPort = int32(from.Port)
		}

		key := fmt.Sprintf("%s@%s:%d", gameName, masterIP, masterPort)
		cp := *g
		ensureMasterHasAddr(&cp, masterIP, masterPort)

		prev, ok := l.games[key]
		if !ok {
			l.games[key] = &cp
			changed = true
		} else {
			*prev = cp
			changed = true
		}
	}
}

func (l *Lobby) emitUpdate() {
	list := make([]*domain.GameAnnouncement, 0, len(l.games))
	for _, g := range l.games {
		list = append(list, g)
	}
	l.onUpdate(list)
}

func extractMasterAddr(g *domain.GameAnnouncement) (string, int32) {
	if g == nil || g.Players == nil {
		return "", 0
	}
	for _, p := range g.Players.Players {
		if p != nil && p.GetRole() == domain.NodeRole_MASTER {
			return p.GetIpAddress(), p.GetPort()
		}
	}
	return "", 0
}

func ensureMasterHasAddr(g *domain.GameAnnouncement, ip string, port int32) {
	if g == nil || g.Players == nil {
		return
	}
	for _, p := range g.Players.Players {
		if p == nil {
			continue
		}
		if p.GetRole() == domain.NodeRole_MASTER {
			if p.IpAddress == nil || p.GetIpAddress() == "" {
				p.IpAddress = domain.StringPtr(ip)
			}
			if p.Port == nil || p.GetPort() == 0 {
				p.Port = domain.Int32Ptr(port)
			}
		}
	}
}
