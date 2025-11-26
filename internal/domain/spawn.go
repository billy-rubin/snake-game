// internal/domain/spawn.go
package domain

import "fmt"

var ErrNoSpaceForNewSnake = fmt.Errorf("domain: no space to place new snake")

// SpawnSnakeResult — результат размещения новой змейки.
type SpawnSnakeResult struct {
	Snake   *GameState_Snake
	Head    Cell
	Tail    Cell
	Players *GamePlayers // тот же объект, для удобства чейнинга
}

// SpawnNewSnake пытается разместить змею нового игрока в соответствии
// с правилом "квадрат 5x5 без змей, голова в центре, хвост — в одну из
// четырёх соседних клеток без еды".
//
// Если подходящее место найти не удалось, возвращает ErrNoSpaceForNewSnake.
func SpawnNewSnake(cfg *GameConfig, state *GameState, playerID int32, rng Random) (*SpawnSnakeResult, error) {
	size := BoardSizeFromConfig(cfg)

	// 1. Заполняем occupancy по змеям (все клетки, занятые любой змеёй).
	occupiedBySnake := make(map[Cell]struct{})

	for _, s := range state.GetSnakes() {
		for _, c := range SnakeCells(s, size) {
			occupiedBySnake[c] = struct{}{}
		}
	}

	// 2. Еда на поле.
	foodCells := make(map[Cell]struct{})
	for _, f := range state.GetFoods() {
		foodCells[CoordToCell(f)] = struct{}{}
	}

	type square struct {
		X0 int32
		Y0 int32
	}

	var candidates []square

	// 3. Находим все квадраты 5x5 без змеек.
	for x0 := int32(0); x0 < size.Width; x0++ {
		for y0 := int32(0); y0 < size.Height; y0++ {
			ok := true
			for dx := int32(0); dx < 5 && ok; dx++ {
				for dy := int32(0); dy < 5; dy++ {
					c := Cell{
						X: Wrap(x0+dx, size.Width),
						Y: Wrap(y0+dy, size.Height),
					}
					if _, busy := occupiedBySnake[c]; busy {
						ok = false
						break
					}
				}
			}
			if ok {
				candidates = append(candidates, square{X0: x0, Y0: y0})
			}
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNoSpaceForNewSnake
	}

	// 4. Перемешивать кандидатов можно, но достаточно случайно выбирать.
	// Пробуем все кандидаты в псевдослучайном порядке.
	startIdx := rng.Intn(len(candidates))

	tryCount := len(candidates)
	for i := 0; i < tryCount; i++ {
		sq := candidates[(startIdx+i)%len(candidates)]
		// Центр квадрата 5x5.
		head := Cell{
			X: Wrap(sq.X0+2, size.Width),
			Y: Wrap(sq.Y0+2, size.Height),
		}

		// Эту клетку тоже нельзя, если там еда.
		if _, hasFood := foodCells[head]; hasFood {
			continue
		}

		// 4 соседа головы.
		type candidateTail struct {
			cell Cell
			dir  Direction
		}

		var tails []candidateTail
		for _, dir := range []Direction{
			Direction_UP,
			Direction_DOWN,
			Direction_LEFT,
			Direction_RIGHT,
		} {
			dx, dy := DirectionDelta(dir)
			tail := Cell{
				X: Wrap(head.X+dx, size.Width),
				Y: Wrap(head.Y+dy, size.Height),
			}
			if _, busy := occupiedBySnake[tail]; busy {
				continue
			}
			if _, hasFood := foodCells[tail]; hasFood {
				continue
			}
			tails = append(tails, candidateTail{cell: tail, dir: dir})
		}

		if len(tails) == 0 {
			// В этом квадрате нельзя подобрать хвост без еды.
			continue
		}

		// Выбираем случайный хвост.
		t := tails[rng.Intn(len(tails))]

		// По условию: направление движения головы ПРОТИВОПОЛОЖНО
		// выбранному направлению хвоста.
		headDir := OppositeDirection(t.dir)

		cells := []Cell{head, t.cell}
		snake, err := EncodeSnakeFromCells(playerID, cells, headDir, GameState_Snake_ALIVE, size)
		if err != nil {
			return nil, err
		}

		// Добавляем змейку в состояние.
		state.Snakes = append(state.Snakes, snake)

		return &SpawnSnakeResult{
			Snake:   snake,
			Head:    head,
			Tail:    t.cell,
			Players: state.GetPlayers(),
		}, nil
	}

	return nil, ErrNoSpaceForNewSnake
}
