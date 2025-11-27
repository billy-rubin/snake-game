// internal/domain/snake.go
package domain

import (
	"fmt"
)

// SnakeCells декодирует представление "ключевых точек" змеи в полный
// список занятых клеток (от головы к хвосту).
//
// Предполагается, что первая точка в Points — абсолютная координата головы,
// а каждая последующая — смещение (deltaX, deltaY) от предыдущей "ключевой"
// точки. При этом смещение может быть длиной > 1 клетки; мы раскрываем
// этот отрезок в соответствующее число шагов вдоль оси.
//
// Алгоритм совместим и с представлением, когда каждое смещение — ровно один шаг.
func SnakeCells(s *GameState_Snake, size BoardSize) []Cell {
	if s == nil || len(s.GetPoints()) == 0 {
		return nil
	}

	points := s.GetPoints()
	head := WrapCell(CoordToCell(points[0]), size)

	cells := make([]Cell, 0, len(points))
	cells = append(cells, head)
	cur := head

	for i := 1; i < len(points); i++ {
		p := points[i]
		dx := p.GetX()
		dy := p.GetY()

		steps := abs32(dx) + abs32(dy)
		if steps == 0 {
			continue
		}

		var stepX, stepY int32
		if dx != 0 {
			if dx > 0 {
				stepX = 1
			} else {
				stepX = -1
			}
		}
		if dy != 0 {
			if dy > 0 {
				stepY = 1
			} else {
				stepY = -1
			}
		}

		for k := int32(0); k < steps; k++ {
			cur.X = Wrap(cur.X+stepX, size.Width)
			cur.Y = Wrap(cur.Y+stepY, size.Height)
			cells = append(cells, cur)
		}
	}

	return cells
}

// EncodeSnakeFromCells кодирует змею из списка клеток (от головы к хвосту)
// обратно в protobuf-представление GameState_Snake.
//
// Мы используем "плотное" представление — каждый шаг сохраняется как
// отдельная точка-смещение (deltaX, deltaY) длиной ровно в одну клетку.
// Это полностью соответствует протоколу, но проще и однозначно.
func EncodeSnakeFromCells(playerID int32, cells []Cell, headDir Direction, state GameState_Snake_SnakeState, size BoardSize) (*GameState_Snake, error) {
	if len(cells) == 0 {
		return nil, fmt.Errorf("EncodeSnakeFromCells: empty cells")
	}

	pts := make([]*GameState_Coord, 0, len(cells))

	// Первая точка — абсолютные координаты головы
	head := WrapCell(cells[0], size)
	pts = append(pts, CellToCoord(head))

	// Остальные — по одному шагу-смещению к следующей клетке
	for i := 1; i < len(cells); i++ {
		prev := WrapCell(cells[i-1], size)
		cur := WrapCell(cells[i], size)

		var dx, dy int32

		// Определяем, куда мы шагнули, учитывая тор.
		if cur.X == Wrap(prev.X+1, size.Width) && cur.Y == prev.Y {
			dx, dy = 1, 0
		} else if cur.X == Wrap(prev.X-1, size.Width) && cur.Y == prev.Y {
			dx, dy = -1, 0
		} else if cur.Y == Wrap(prev.Y+1, size.Height) && cur.X == prev.X {
			dx, dy = 0, 1
		} else if cur.Y == Wrap(prev.Y-1, size.Height) && cur.X == prev.X {
			dx, dy = 0, -1
		} else {
			return nil, fmt.Errorf("EncodeSnakeFromCells: non-adjacent segments: %v -> %v", prev, cur)
		}

		pts = append(pts, &GameState_Coord{
			X: int32p(dx),
			Y: int32p(dy),
		})
	}

	return &GameState_Snake{
		PlayerId:      int32p(playerID),
		Points:        pts,
		State:         &state,
		HeadDirection: &headDir,
	}, nil
}

// SnakeHeadCell возвращает клетку головы змейки.
func SnakeHeadCell(s *GameState_Snake, size BoardSize) Cell {
	if s == nil || len(s.GetPoints()) == 0 {
		return Cell{}
	}
	return WrapCell(CoordToCell(s.GetPoints()[0]), size)
}

// SnakeLength возвращает длину змейки в клетках.
func SnakeLength(s *GameState_Snake, size BoardSize) int {
	return len(SnakeCells(s, size))
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
