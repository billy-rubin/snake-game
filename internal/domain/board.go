package domain

// Cell — внутренняя "ячейка" для работы с координатами на поле.
type Cell struct {
	X int32
	Y int32
}

// Wrap нормализует координату по размеру поля (дискретный тор).
func Wrap(x int32, size int32) int32 {
	if size <= 0 {
		return x
	}
	x %= size
	if x < 0 {
		x += size
	}
	return x
}

// WrapCell приводит координаты клетки к допустимому диапазону.
func WrapCell(c Cell, size BoardSize) Cell {
	return Cell{
		X: Wrap(c.X, size.Width),
		Y: Wrap(c.Y, size.Height),
	}
}

// CoordToCell конвертирует protobuf-координату в Cell.
func CoordToCell(c *GameState_Coord) Cell {
	if c == nil {
		return Cell{}
	}
	return Cell{
		X: c.GetX(),
		Y: c.GetY(),
	}
}

// CellToCoord конвертирует Cell обратно в protobuf-координату.
func CellToCoord(c Cell) *GameState_Coord {
	return &GameState_Coord{
		X: int32p(c.X),
		Y: int32p(c.Y),
	}
}

// DirectionDelta возвращает сдвиг по X/Y для заданного направления.
func DirectionDelta(dir Direction) (dx, dy int32) {
	switch dir {
	case Direction_UP:
		return 0, -1
	case Direction_DOWN:
		return 0, 1
	case Direction_LEFT:
		return -1, 0
	case Direction_RIGHT:
		return 1, 0
	default:
		return 0, 0
	}
}

// OppositeDirection возвращает направление, противоположное dir.
func OppositeDirection(dir Direction) Direction {
	switch dir {
	case Direction_UP:
		return Direction_DOWN
	case Direction_DOWN:
		return Direction_UP
	case Direction_LEFT:
		return Direction_RIGHT
	case Direction_RIGHT:
		return Direction_LEFT
	default:
		return dir
	}
}
