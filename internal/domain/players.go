package domain

func NewPlayer(id int32, name string, role NodeRole, ptype PlayerType) *GamePlayer {
	return &GamePlayer{
		Name:  stringp(name),
		Id:    int32p(id),
		Role:  &role,
		Type:  &ptype,
		Score: int32p(0),
	}
}

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
