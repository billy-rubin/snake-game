package domain

import (
	"fmt"
)

type TickResult struct {
	DeadPlayers          []int32
	FoodEatenByPlayer    map[int32]int
	KillsByPlayer        map[int32]int
	AliveSnakesAfterTick int
}

func ApplyTick(cfg *GameConfig, state *GameState, steers map[int32]Direction, rng Random) (*TickResult, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("ApplyTick: state is nil")
	}
	size := BoardSizeFromConfig(cfg)

	prevOccupancy := make(map[Cell]int32)
	for _, s := range state.GetSnakes() {
		playerID := s.GetPlayerId()
		for _, c := range SnakeCells(s, size) {
			prevOccupancy[c] = playerID
		}
	}

	prevFoods := FoodSet(state)

	players := state.GetPlayers()

	type snakeStep struct {
		playerID int32
		alive    bool
		state    GameState_Snake_SnakeState
		dir      Direction
		cells    []Cell
	}

	steps := make(map[int32]*snakeStep)

	for _, s := range state.GetSnakes() {
		playerID := s.GetPlayerId()
		curState := s.GetState()
		curDir := s.GetHeadDirection()
		cells := SnakeCells(s, size)
		if len(cells) == 0 {
			continue
		}
		head := cells[0]

		newDir := curDir
		if curState == GameState_Snake_ALIVE {
			if steer, ok := steers[playerID]; ok && steer != 0 {
				if steer != OppositeDirection(curDir) {
					newDir = steer
				}
			}
		}

		dx, dy := DirectionDelta(newDir)
		newHead := Cell{
			X: Wrap(head.X+dx, size.Width),
			Y: Wrap(head.Y+dy, size.Height),
		}

		_, hadFood := prevFoods[newHead]

		var newCells []Cell
		newCells = append(newCells, newHead)

		if hadFood {
			newCells = append(newCells, cells...)
		} else {
			if len(cells) > 1 {
				newCells = append(newCells, cells[:len(cells)-1]...)
			}
		}

		steps[playerID] = &snakeStep{
			playerID: playerID,
			alive:    curState == GameState_Snake_ALIVE,
			state:    curState,
			dir:      newDir,
			cells:    newCells,
		}
	}

	type cellOcc struct {
		Heads  []int32
		Bodies []int32
	}

	newOccupancy := make(map[Cell]*cellOcc)

	for _, st := range steps {
		if st == nil || len(st.cells) == 0 {
			continue
		}
		for i, c := range st.cells {
			occ := newOccupancy[c]
			if occ == nil {
				occ = &cellOcc{}
				newOccupancy[c] = occ
			}
			if i == 0 {
				occ.Heads = append(occ.Heads, st.playerID)
			} else {
				occ.Bodies = append(occ.Bodies, st.playerID)
			}
		}
	}

	dead := make(map[int32]bool) // playerID -> dead?

	for pid, st := range steps {
		if st == nil || len(st.cells) == 0 {
			continue
		}
		headCell := st.cells[0]
		occ := newOccupancy[headCell]
		if occ == nil {
			continue
		}

		if len(occ.Heads) > 1 {
			dead[pid] = true
			continue
		}
		if len(occ.Bodies) > 0 {
			dead[pid] = true
			continue
		}
	}

	foodScores := make(map[int32]int)
	killScores := make(map[int32]int)

	for pid, st := range steps {
		if st == nil || len(st.cells) == 0 {
			continue
		}
		headCell := st.cells[0]

		if _, hadFood := prevFoods[headCell]; hadFood {
			foodScores[pid]++
		}

		if dead[pid] {
			if victimOwner, ok := prevOccupancy[headCell]; ok && victimOwner != pid {
				killScores[victimOwner]++
			}
		}
	}

	if players != nil {
		for _, p := range players.GetPlayers() {
			id := p.GetId()
			score := p.GetScore()
			if score < 0 {
				score = 0
			}
			if add, ok := foodScores[id]; ok && add > 0 {
				score += int32(add)
			}
			if add, ok := killScores[id]; ok && add > 0 {
				score += int32(add)
			}
			p.Score = int32p(score)
		}
	}

	var deadPlayers []int32
	if players != nil {
		for _, p := range players.GetPlayers() {
			pid := p.GetId()
			if dead[pid] {
				role := NodeRole_VIEWER
				p.Role = &role
				deadPlayers = append(deadPlayers, pid)
			}
		}
	}

	foodsNew := make(map[Cell]struct{}, len(prevFoods))

	for c := range prevFoods {
		foodsNew[c] = struct{}{}
	}

	for pid, st := range steps {
		if st == nil || len(st.cells) == 0 {
			continue
		}
		headCell := st.cells[0]
		if _, ate := prevFoods[headCell]; ate {
			delete(foodsNew, headCell)
			_ = pid
		}
	}

	survivorSnakes := make([]*GameState_Snake, 0, len(state.GetSnakes()))
	occupiedBySnakes := make(map[Cell]struct{})

	aliveCount := 0

	for _, s := range state.GetSnakes() {
		pid := s.GetPlayerId()
		if dead[pid] {
			continue
		}
		st := steps[pid]
		if st == nil || len(st.cells) == 0 {
			continue
		}

		newSnake, err := EncodeSnakeFromCells(pid, st.cells, st.dir, s.GetState(), size)
		if err != nil {
			return nil, err
		}
		if newSnake.GetState() == GameState_Snake_ALIVE {
			aliveCount++
		}

		for _, c := range st.cells {
			occupiedBySnakes[c] = struct{}{}
		}

		survivorSnakes = append(survivorSnakes, newSnake)
	}

	for pid, st := range steps {
		if !dead[pid] || st == nil {
			continue
		}
		for _, c := range st.cells {
			delete(foodsNew, c)
			if rng.Float64() < 0.5 {
				foodsNew[c] = struct{}{}
			}
		}
	}

	targetFood := int(cfg.GetFoodStatic()) + aliveCount
	if targetFood < 0 {
		targetFood = 0
	}

	var emptyCells []Cell
	for x := int32(0); x < size.Width; x++ {
		for y := int32(0); y < size.Height; y++ {
			c := Cell{X: x, Y: y}
			if _, busy := occupiedBySnakes[c]; busy {
				continue
			}
			if _, hasFood := foodsNew[c]; hasFood {
				continue
			}
			emptyCells = append(emptyCells, c)
		}
	}

	needFood := targetFood - len(foodsNew)
	if needFood > len(emptyCells) {
		needFood = len(emptyCells)
	}
	for i := 0; i < needFood; i++ {
		idx := rng.Intn(len(emptyCells))
		c := emptyCells[idx]
		foodsNew[c] = struct{}{}
		emptyCells[idx] = emptyCells[len(emptyCells)-1]
		emptyCells = emptyCells[:len(emptyCells)-1]
	}

	state.Snakes = survivorSnakes
	UpdateFoods(state, foodsNew)

	res := &TickResult{
		DeadPlayers:          deadPlayers,
		FoodEatenByPlayer:    foodScores,
		KillsByPlayer:        killScores,
		AliveSnakesAfterTick: aliveCount,
	}
	return res, nil
}
