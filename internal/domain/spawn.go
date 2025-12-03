// internal/domain/spawn.go
package domain

import (
	"fmt"
)

var ErrNoSpaceForNewSnake = fmt.Errorf("domain: no space to place new snake")

type SpawnSnakeResult struct {
	Snake   *GameState_Snake
	Head    Cell
	Tail    Cell
	Players *GamePlayers
}

func SpawnNewSnake(cfg *GameConfig, state *GameState, playerID int32, rng Random) (*SpawnSnakeResult, error) {
	size := BoardSizeFromConfig(cfg)

	occupiedBySnake := make(map[Cell]struct{})

	for _, s := range state.GetSnakes() {
		for _, c := range SnakeCells(s, size) {
			occupiedBySnake[c] = struct{}{}
		}
	}

	foodCells := make(map[Cell]struct{})
	for _, f := range state.GetFoods() {
		foodCells[CoordToCell(f)] = struct{}{}
	}

	type square struct {
		X0 int32
		Y0 int32
	}

	var candidates []square

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

	startIdx := rng.Intn(len(candidates))

	tryCount := len(candidates)
	for i := 0; i < tryCount; i++ {
		sq := candidates[(startIdx+i)%len(candidates)]
		head := Cell{
			X: Wrap(sq.X0+2, size.Width),
			Y: Wrap(sq.Y0+2, size.Height),
		}

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
			continue
		}

		t := tails[rng.Intn(len(tails))]

		headDir := OppositeDirection(t.dir)

		cells := []Cell{head, t.cell}
		snake, err := EncodeSnakeFromCells(playerID, cells, headDir, GameState_Snake_ALIVE, size)
		if err != nil {
			return nil, err
		}

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
