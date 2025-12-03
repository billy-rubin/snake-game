package network

import (
	"fmt"
	"google.golang.org/protobuf/proto"
	"net"
	"snake-game/internal/domain"
)

type MulticastListener struct {
	conn *net.UDPConn
	addr *net.UDPAddr
}

func NewMulticastListener() (*MulticastListener, error) {
	group, err := net.ResolveUDPAddr("udp4", MulticastAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve multicast addr: %w", err)
	}

	c, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, fmt.Errorf("listen multicast udp: %w", err)
	}

	return &MulticastListener{
		conn: c,
		addr: group,
	}, nil
}

func (m *MulticastListener) Close() error {
	return m.conn.Close()
}

func (m *MulticastListener) GroupAddr() *net.UDPAddr {
	return m.addr
}

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

func (u *UnicastConn) SendMulticast(msg *domain.GameMessage) error {
	group, err := net.ResolveUDPAddr("udp4", MulticastAddress)
	if err != nil {
		return fmt.Errorf("resolve multicast addr: %w", err)
	}
	return u.Send(msg, group)
}
