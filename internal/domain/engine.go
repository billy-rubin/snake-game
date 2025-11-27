package domain

import (
	"fmt"
)

// TickResult — агрегированная информация о результате хода.
// Состояние игры уже изменено внутри GameState.
type TickResult struct {
	DeadPlayers          []int32       // игроки, чьи змейки погибли на этом ходу
	FoodEatenByPlayer    map[int32]int // сколько кусков еды съел каждый игрок
	KillsByPlayer        map[int32]int // сколько "убийств" (врезались в их змейку)
	AliveSnakesAfterTick int           // количество ALIVE-змеек после хода
}

// ApplyTick применяет один "ход" игры к состоянию state по правилам задачи.
// steers — карта playerID -> новое направление (последняя команда за этот ход).
// rng — источник случайности для генерации еды из трупов и "статической" еды.
func ApplyTick(cfg *GameConfig, state *GameState, steers map[int32]Direction, rng Random) (*TickResult, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("ApplyTick: state is nil")
	}
	size := BoardSizeFromConfig(cfg)

	// Предыдущее распределение змей по клеткам (для начисления очков "за столкновение").
	prevOccupancy := make(map[Cell]int32) // cell -> playerID
	for _, s := range state.GetSnakes() {
		playerID := s.GetPlayerId()
		for _, c := range SnakeCells(s, size) {
			prevOccupancy[c] = playerID
		}
	}

	// Еда до хода.
	prevFoods := FoodSet(state)

	// Игроки для обновления очков и ролей.
	players := state.GetPlayers()

	// Новые позиции змей после хода.
	type snakeStep struct {
		playerID int32
		alive    bool
		state    GameState_Snake_SnakeState
		dir      Direction
		cells    []Cell // новые клетки змеи (голова -> хвост)
	}

	steps := make(map[int32]*snakeStep) // playerID -> step

	// 1. Движение змей (но пока без учёта столкновений).
	for _, s := range state.GetSnakes() {
		playerID := s.GetPlayerId()
		curState := s.GetState()
		curDir := s.GetHeadDirection()
		cells := SnakeCells(s, size)
		if len(cells) == 0 {
			continue
		}
		head := cells[0]

		// Вычисляем новое направление (для ALIVE-змей).
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

		// Проверяем, есть ли еда в целевой клетке.
		_, hadFood := prevFoods[newHead]

		var newCells []Cell
		newCells = append(newCells, newHead)

		if hadFood {
			// Змейка растёт: голова сдвигается вперёд, а тело остаётся на месте.
			newCells = append(newCells, cells...)
		} else {
			// Обычный шаг: сдвиг головы + сдвиг тела, хвост освобождается.
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

	// 2. Строим occupancy по НОВОМУ состоянию змей — для определения столкновений.
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

	// 3. Определяем, какие змейки погибают.
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

		// Правила:
		// - если на одну клетку приехало несколько голов -> все эти змейки погибают;
		// - если голова оказалась в клетке, занятой туловищем любой змейки (своей или чужой) -> погибает.
		if len(occ.Heads) > 1 {
			dead[pid] = true
			continue
		}
		if len(occ.Bodies) > 0 {
			dead[pid] = true
			continue
		}
	}

	// 4. Начисление очков за еду и "убийства".
	foodScores := make(map[int32]int)
	killScores := make(map[int32]int)

	for pid, st := range steps {
		if st == nil || len(st.cells) == 0 {
			continue
		}
		headCell := st.cells[0]

		// Если съел еду — +1 балл.
		if _, hadFood := prevFoods[headCell]; hadFood {
			foodScores[pid]++
		}

		// Если змея погибла — проверяем, в кого она "врезалась" по прошлому состоянию.
		if dead[pid] {
			if victimOwner, ok := prevOccupancy[headCell]; ok && victimOwner != pid {
				killScores[victimOwner]++
			}
		}
	}

	// 5. Обновляем счёт игроков.
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

	// 6. Обновляем роли игроков, чьи змейки погибли (они "выбывают" и становятся VIEWER).
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

	// 7. Формируем новую еду: убираем съеденную, добавляем еду из трупов, затем добиваем до
	// количества (food_static + число ALIVE-змеек), если есть свободные клетки.
	foodsNew := make(map[Cell]struct{}, len(prevFoods))

	// Начинаем с прошлой еды.
	for c := range prevFoods {
		foodsNew[c] = struct{}{}
	}

	// Убираем съеденную еду.
	for pid, st := range steps {
		if st == nil || len(st.cells) == 0 {
			continue
		}
		headCell := st.cells[0]
		if _, ate := prevFoods[headCell]; ate {
			delete(foodsNew, headCell)
			_ = pid // pid уже учтён в foodScores
		}
	}

	// Теперь формируем список живых/зомби-змей и занимаемых ими клеток.
	survivorSnakes := make([]*GameState_Snake, 0, len(state.GetSnakes()))
	occupiedBySnakes := make(map[Cell]struct{})

	aliveCount := 0

	for _, s := range state.GetSnakes() {
		pid := s.GetPlayerId()
		if dead[pid] {
			continue // эту змею удаляем полностью
		}
		st := steps[pid]
		if st == nil || len(st.cells) == 0 {
			continue
		}

		// Заново кодируем змею из новых клеток.
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

	// 8. Генерация еды из трупов (каждая клетка погибшей змейки -> еда с p=0.5).
	for pid, st := range steps {
		if !dead[pid] || st == nil {
			continue
		}
		for _, c := range st.cells {
			// Сначала очищаем от возможной еды.
			delete(foodsNew, c)
			if rng.Float64() < 0.5 {
				foodsNew[c] = struct{}{}
			}
		}
	}

	// 9. Добиваем количество еды до food_static + aliveCount (если есть свободные клетки).
	targetFood := int(cfg.GetFoodStatic()) + aliveCount
	if targetFood < 0 {
		targetFood = 0
	}

	// Строим список пустых клеток (без змей и еды).
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
		// Берём случайную пустую клетку и удаляем её из списка.
		idx := rng.Intn(len(emptyCells))
		c := emptyCells[idx]
		foodsNew[c] = struct{}{}
		// Удаляем выбранную клетку из слайса так, чтобы не оставлять дырок.
		emptyCells[idx] = emptyCells[len(emptyCells)-1]
		emptyCells = emptyCells[:len(emptyCells)-1]
	}

	// 10. Обновляем состояние.
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
