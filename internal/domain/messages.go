package domain

// Вспомогательные функции-указатели.
func Int64Ptr(v int64) *int64                { return &v }
func Int32Ptr(v int32) *int32                { return &v }
func StringPtr(v string) *string             { return &v }
func NodeRolePtr(r NodeRole) *NodeRole       { return &r }
func PlayerTypePtr(t PlayerType) *PlayerType { return &t }

// базовый конструктор GameMessage c общим прологом.
func newBaseGameMessage(msgSeq int64, senderId, receiverId *int32) *GameMessage {
	m := &GameMessage{
		MsgSeq: Int64Ptr(msgSeq),
	}
	if senderId != nil {
		m.SenderId = senderId
	}
	if receiverId != nil {
		m.ReceiverId = receiverId
	}
	return m
}

// ---------- PING ----------

func NewPingMessage(msgSeq int64, senderId, receiverId int32) *GameMessage {
	m := newBaseGameMessage(msgSeq, Int32Ptr(senderId), Int32Ptr(receiverId))
	m.Type = &GameMessage_Ping{
		Ping: &GameMessage_PingMsg{},
	}
	return m
}

// ---------- STEER ----------

func NewSteerMessage(msgSeq int64, senderId int32, direction Direction) *GameMessage {
	m := newBaseGameMessage(msgSeq, Int32Ptr(senderId), nil)
	m.Type = &GameMessage_Steer{
		Steer: &GameMessage_SteerMsg{
			Direction: &direction,
		},
	}
	return m
}

// ---------- ACK ----------

func NewAckMessage(msgSeq int64, senderId, receiverId int32) *GameMessage {
	m := newBaseGameMessage(msgSeq, Int32Ptr(senderId), Int32Ptr(receiverId))
	m.Type = &GameMessage_Ack{
		Ack: &GameMessage_AckMsg{},
	}
	return m
}

// ---------- STATE ----------

func NewStateMessage(msgSeq int64, senderId int32, state *GameState) *GameMessage {
	m := newBaseGameMessage(msgSeq, Int32Ptr(senderId), nil)
	m.Type = &GameMessage_State{
		State: &GameMessage_StateMsg{
			State: state,
		},
	}
	return m
}

// ---------- ANNOUNCEMENT ----------

func NewAnnouncementMessage(msgSeq int64, senderId int32, games []*GameAnnouncement) *GameMessage {
	m := newBaseGameMessage(msgSeq, Int32Ptr(senderId), nil)
	m.Type = &GameMessage_Announcement{
		Announcement: &GameMessage_AnnouncementMsg{
			Games: games,
		},
	}
	return m
}

func NewSingleGameAnnouncementMessage(msgSeq int64, senderId int32, game *GameAnnouncement) *GameMessage {
	return NewAnnouncementMessage(msgSeq, senderId, []*GameAnnouncement{game})
}

// ---------- JOIN ----------

func NewJoinMessage(
	msgSeq int64,
	senderId int32,
	playerType PlayerType,
	playerName string,
	gameName string,
	requestedRole NodeRole,
) *GameMessage {
	m := newBaseGameMessage(msgSeq, Int32Ptr(senderId), nil)
	m.Type = &GameMessage_Join{
		Join: &GameMessage_JoinMsg{
			PlayerType:    PlayerTypePtr(playerType),
			PlayerName:    StringPtr(playerName),
			GameName:      StringPtr(gameName),
			RequestedRole: NodeRolePtr(requestedRole),
		},
	}
	return m
}

// ---------- ERROR ----------

func NewErrorMessage(
	msgSeq int64,
	senderId, receiverId int32,
	text string,
) *GameMessage {
	m := newBaseGameMessage(msgSeq, Int32Ptr(senderId), Int32Ptr(receiverId))
	m.Type = &GameMessage_Error{
		Error: &GameMessage_ErrorMsg{
			ErrorMessage: StringPtr(text),
		},
	}
	return m
}

// ---------- ROLE CHANGE ----------

func NewRoleChangeMessage(
	msgSeq int64,
	senderId, receiverId int32,
	senderRole, receiverRole *NodeRole, // могут быть nil по протоколу
) *GameMessage {
	m := newBaseGameMessage(msgSeq, Int32Ptr(senderId), Int32Ptr(receiverId))

	rc := &GameMessage_RoleChangeMsg{}
	if senderRole != nil {
		rc.SenderRole = NodeRolePtr(*senderRole)
	}
	if receiverRole != nil {
		rc.ReceiverRole = NodeRolePtr(*receiverRole)
	}

	m.Type = &GameMessage_RoleChange{
		RoleChange: rc,
	}
	return m
}

// ---------- DISCOVER ----------

func NewDiscoverMessage(msgSeq int64, senderId int32) *GameMessage {
	m := newBaseGameMessage(msgSeq, Int32Ptr(senderId), nil)
	m.Type = &GameMessage_Discover{
		Discover: &GameMessage_DiscoverMsg{},
	}
	return m
}
