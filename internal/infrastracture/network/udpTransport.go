package network

import (
	"fmt"
	"net"
	"time"

	"google.golang.org/protobuf/proto"

	"snake-game/internal/domain"
)

// Multicast-адрес для Announcement/Discover,
// строго по условию задачи.
const (
	MulticastAddress = "239.192.0.4:9192"
	// Безопасный верхний предел для UDP-пакета.
	MaxDatagramSize = 64 * 1024
)

// Envelope – обёртка "сообщение + адрес отправителя".
type Envelope struct {
	Msg  *domain.GameMessage
	From *net.UDPAddr
}

// ---------- Unicast сокет (вся игровая логика) ----------

type UnicastConn struct {
	conn *net.UDPConn
}

// NewUnicastConn создаёт обычный UDP-сокет.
// Если laddr == nil, ОС сама выберет порт.
func NewUnicastConn(laddr *net.UDPAddr) (*UnicastConn, error) {
	if laddr == nil {
		laddr = &net.UDPAddr{
			IP:   net.IPv4zero,
			Port: 0,
		}
	}

	c, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		return nil, fmt.Errorf("listen udp: %w", err)
	}

	return &UnicastConn{conn: c}, nil
}

func (u *UnicastConn) Close() error {
	return u.conn.Close()
}

// LocalAddr возвращает реальный адрес сокета (с выбранным портом).
func (u *UnicastConn) LocalAddr() *net.UDPAddr {
	if u == nil || u.conn == nil {
		return nil
	}
	if addr, ok := u.conn.LocalAddr().(*net.UDPAddr); ok {
		return addr
	}
	return nil
}

// Send сериализует GameMessage и отправляет его одному получателю.
func (u *UnicastConn) Send(msg *domain.GameMessage, to *net.UDPAddr) error {
	if msg == nil {
		return fmt.Errorf("nil GameMessage")
	}
	if to == nil {
		return fmt.Errorf("nil destination address")
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal GameMessage: %w", err)
	}

	_, err = u.conn.WriteToUDP(data, to)
	if err != nil {
		return fmt.Errorf("write udp: %w", err)
	}
	return nil
}

// ReceiveOne блокирующе читает один UDP-датаграмм и декодирует GameMessage.
func (u *UnicastConn) ReceiveOne() (*Envelope, error) {
	buf := make([]byte, MaxDatagramSize)

	n, from, err := u.conn.ReadFromUDP(buf)
	if err != nil {
		return nil, fmt.Errorf("read udp: %w", err)
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

// ReceiveOneWithTimeout читает один UDP-пакет с тайм-аутом.
// При истечении тайм-аута вернёт ошибку net.Error с Timeout() == true.
func (u *UnicastConn) ReceiveOneWithTimeout(timeout time.Duration) (*Envelope, error) {
	if err := u.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}

	buf := make([]byte, MaxDatagramSize)

	n, from, err := u.conn.ReadFromUDP(buf)
	if err != nil {
		return nil, err
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
