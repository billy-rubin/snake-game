package domain

func AliveSnakes(state *GameState) []*GameState_Snake {
	if state == nil {
		return nil
	}
	var res []*GameState_Snake
	for _, s := range state.GetSnakes() {
		if s.GetState() == GameState_Snake_ALIVE {
			res = append(res, s)
		}
	}
	return res
}

func FoodSet(state *GameState) map[Cell]struct{} {
	foods := make(map[Cell]struct{})
	if state == nil {
		return foods
	}
	for _, f := range state.GetFoods() {
		foods[CoordToCell(f)] = struct{}{}
	}
	return foods
}

func UpdateFoods(state *GameState, foods map[Cell]struct{}) {
	state.Foods = state.Foods[:0]
	for c := range foods {
		state.Foods = append(state.Foods, CellToCoord(c))
	}
}
