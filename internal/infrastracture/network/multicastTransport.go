package network

import (
	"fmt"
	"google.golang.org/protobuf/proto"
	"net"
	"snake-game/internal/domain"
)

// ---------- Multicast приёмник для Announcement/Discover ----------

type MulticastListener struct {
	conn *net.UDPConn
	addr *net.UDPAddr
}

// NewMulticastListener открывает сокет, слушающий 239.192.0.4:9192.
// По протоколу из него ТОЛЬКО читаем (а посылаем анонсы обычным UnicastConn,
// указав multicast-адрес как dest).
func NewMulticastListener() (*MulticastListener, error) {
	group, err := net.ResolveUDPAddr("udp4", MulticastAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve multicast addr: %w", err)
	}

	// ListenMulticastUDP автоматически подключает сокет к группе на всех интерфейсах.
	// При необходимости потом можно будет сузить до конкретного интерфейса.
	c, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, fmt.Errorf("listen multicast udp: %w", err)
	}

	// Рекомендуется явно выставить размер буфера, но пока можно оставить по умолчанию.

	return &MulticastListener{
		conn: c,
		addr: group,
	}, nil
}

func (m *MulticastListener) Close() error {
	return m.conn.Close()
}

// GroupAddr возвращает адрес multicast-группы.
func (m *MulticastListener) GroupAddr() *net.UDPAddr {
	return m.addr
}

// ReceiveOne блокирующе читает один пакет с multicast-сокета.
func (m *MulticastListener) ReceiveOne() (*Envelope, error) {
	buf := make([]byte, MaxDatagramSize)

	n, from, err := m.conn.ReadFromUDP(buf)
	if err != nil {
		return nil, fmt.Errorf("read multicast udp: %w", err)
	}

	var msg domain.GameMessage
	if err := proto.Unmarshal(buf[:n], &msg); err != nil {
		return nil, fmt.Errorf("unmarshal GameMessage: %w", err)
	}

	return &Envelope{
		Msg:  &msg,
		From: from,
	}, nil
}

// SendMulticast отправляет GameMessage (обычно Announcement/Discover)
// в мультикаст-группу.
func (u *UnicastConn) SendMulticast(msg *domain.GameMessage) error {
	group, err := net.ResolveUDPAddr("udp4", MulticastAddress)
	if err != nil {
		return fmt.Errorf("resolve multicast addr: %w", err)
	}
	return u.Send(msg, group)
}
