package domain

// NewPlayer создаёт объект GamePlayer с заданными полями.
// IpAddress/Port заполняются позже сетевым слоем.
func NewPlayer(id int32, name string, role NodeRole, ptype PlayerType) *GamePlayer {
	return &GamePlayer{
		Name:  stringp(name),
		Id:    int32p(id),
		Role:  &role,
		Type:  &ptype,
		Score: int32p(0),
	}
}

// GetPlayerByID ищет игрока по идентификатору в GamePlayers.
func GetPlayerByID(players *GamePlayers, id int32) *GamePlayer {
	if players == nil {
		return nil
	}
	for _, p := range players.GetPlayers() {
		if p.GetId() == id {
			return p
		}
	}
	return nil
}

// NextPlayerID возвращает следующий свободный идентификатор игрока
// (max(existing)+1). Если игроков нет, возвращает 1.
func NextPlayerID(players *GamePlayers) int32 {
	var maxID int32
	if players != nil {
		for _, p := range players.GetPlayers() {
			if p.GetId() > maxID {
				maxID = p.GetId()
			}
		}
	}
	return maxID + 1
}
