// internal/domain/state.go
package domain

// AliveSnakes возвращает список всех змей в состоянии ALIVE.
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

// AliveSnakesCount возвращает количество змей в состоянии ALIVE.
func AliveSnakesCount(state *GameState) int {
	return len(AliveSnakes(state))
}

// SnakeByPlayerID возвращает змею, принадлежащую игроку playerID (если есть).
func SnakeByPlayerID(state *GameState, playerID int32) *GameState_Snake {
	if state == nil {
		return nil
	}
	for _, s := range state.GetSnakes() {
		if s.GetPlayerId() == playerID {
			return s
		}
	}
	return nil
}

// RemoveSnakeByPlayerID удаляет змею игрока из списка змей.
func RemoveSnakeByPlayerID(state *GameState, playerID int32) {
	if state == nil {
		return
	}
	snakes := state.GetSnakes()
	j := 0
	for _, s := range snakes {
		if s.GetPlayerId() == playerID {
			continue
		}
		snakes[j] = s
		j++
	}
	state.Snakes = snakes[:j]
}

// FoodSet строит множество клеток с едой.
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

// UpdateFoods перезаписывает список Foods на основе множества клеток.
func UpdateFoods(state *GameState, foods map[Cell]struct{}) {
	state.Foods = state.Foods[:0]
	for c := range foods {
		state.Foods = append(state.Foods, CellToCoord(c))
	}
}
