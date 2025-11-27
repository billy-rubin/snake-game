package application

import (
	"fmt"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"snake-game/internal/domain"
)

type MasterController struct {
	engine   *GameEngine
	stateIn  <-chan *domain.GameState
	myID     int32
	cfg      *domain.GameConfig
	gameName string

	app   *tview.Application
	view  *tview.TextView
	state *domain.GameState
	mu    sync.Mutex
}

func NewMasterController(
	gameName string,
	myID int32,
	cfg *domain.GameConfig,
	engine *GameEngine,
	stateIn <-chan *domain.GameState,
) *MasterController {
	return &MasterController{
		gameName: gameName,
		myID:     myID,
		cfg:      cfg,
		engine:   engine,
		stateIn:  stateIn,
	}
}

func (m *MasterController) Run() error {
	m.app = tview.NewApplication()

	m.view = tview.NewTextView().
		SetDynamicColors(true). // Включаем поддержку цветов [red], [green] и т.д.
		SetScrollable(false).
		SetChangedFunc(func() {
			m.app.Draw()
		})

	m.view.SetBorder(true).
		SetTitle(fmt.Sprintf(" HOST: %s ", m.gameName))

	m.view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		var dir domain.Direction
		switch event.Key() {
		case tcell.KeyUp:
			dir = domain.Direction_UP
		case tcell.KeyDown:
			dir = domain.Direction_DOWN
		case tcell.KeyLeft:
			dir = domain.Direction_LEFT
		case tcell.KeyRight:
			dir = domain.Direction_RIGHT
		case tcell.KeyEsc:
			m.app.Stop()
			return nil
		}

		switch event.Rune() {
		case 'w', 'W':
			dir = domain.Direction_UP
		case 's', 'S':
			dir = domain.Direction_DOWN
		case 'a', 'A':
			dir = domain.Direction_LEFT
		case 'd', 'D':
			dir = domain.Direction_RIGHT
		case 'q', 'Q':
			m.app.Stop()
			return nil
		}

		if dir != 0 {
			m.engine.ApplySteer(m.myID, dir)
			return nil
		}
		return event
	})

	m.app.SetRoot(m.view, true)

	go m.listenerLoop()

	return m.app.Run()
}

func (m *MasterController) listenerLoop() {
	for st := range m.stateIn {
		if st == nil {
			continue
		}

		m.mu.Lock()
		m.state = st
		m.mu.Unlock()

		m.app.QueueUpdateDraw(func() {
			m.view.SetText(m.renderState(st))
		})
	}
}

// renderState — ВОЗВРАЩАЕМ СТАРУЮ ГРАФИКУ
func (m *MasterController) renderState(st *domain.GameState) string {
	if st == nil {
		return "Waiting for game start..."
	}
	w := int(m.cfg.GetWidth())
	h := int(m.cfg.GetHeight())
	if w <= 0 {
		w = 40
	}
	if h <= 0 {
		h = 30
	}

	// 1. Создаем "канвас" из пробелов
	field := make([][]rune, h)
	for y := 0; y < h; y++ {
		field[y] = make([]rune, w)
		for x := 0; x < w; x++ {
			field[y][x] = ' ' // Пустое поле - пробел (как в оригинале)
		}
	}

	// 2. Рисуем еду как '*'
	for _, f := range st.GetFoods() {
		x, y := int(f.GetX()), int(f.GetY())
		if x >= 0 && x < w && y >= 0 && y < h {
			field[y][x] = '*'
		}
	}

	// 3. Рисуем змей
	myScore := 0
	myName := "Player"

	// Сначала найдем свои очки для отображения в заголовке
	for _, p := range st.GetPlayers().GetPlayers() {
		if p.GetId() == m.myID {
			myScore = int(p.GetScore())
			myName = p.GetName()
		}
	}

	for _, s := range st.GetSnakes() {
		cells := domain.SnakeCells(s, domain.BoardSize{Width: int32(w), Height: int32(h)})
		isMe := (s.GetPlayerId() == m.myID)
		isZombie := (s.GetState() == domain.GameState_Snake_ZOMBIE)

		for i, cell := range cells {
			cx, cy := int(cell.X), int(cell.Y)
			if cx < 0 || cx >= w || cy < 0 || cy >= h {
				continue
			}

			// Логика символов из local_single_player.go
			var char rune
			if isMe {
				if i == 0 {
					char = 'O' // Моя голова
				} else {
					char = 'o' // Мое тело
				}
			} else if isZombie {
				char = 'Z' // Зомби
			} else {
				// Враги
				if i == 0 {
					char = 'S' // Голова врага
				} else {
					char = 's' // Тело врага
				}
			}
			field[cy][cx] = char
		}
	}

	var b strings.Builder

	// Заголовок в стиле local_single_player
	b.WriteString(fmt.Sprintf(
		"Игра: [white]%s[-]  |  Игрок: [yellow]%s[-]  |  Счёт: [green]%d[-]  |  Размер: %dx%d\n",
		m.gameName, myName, myScore, w, h,
	))
	b.WriteString(fmt.Sprintf("Alive Snakes: %d  |  Food: %d\n\n", len(domain.AliveSnakes(st)), len(st.GetFoods())))

	// Отрисовка поля с рамками
	// Верхняя граница
	b.WriteString("+" + strings.Repeat("-", w) + "+\n")

	for y := 0; y < h; y++ {
		b.WriteString("|")
		for x := 0; x < w; x++ {
			// Красим символы
			r := field[y][x]
			switch r {
			case 'O', 'o':
				b.WriteString("[green]")
				b.WriteRune(r)
				b.WriteString("[-]")
			case 'S', 's':
				b.WriteString("[red]")
				b.WriteRune(r)
				b.WriteString("[-]")
			case '*':
				b.WriteString("[yellow]*[-]")
			case 'Z':
				b.WriteString("[gray]Z[-]")
			default:
				b.WriteRune(r)
			}
		}
		b.WriteString("|\n")
	}

	// Нижняя граница
	b.WriteString("+" + strings.Repeat("-", w) + "+\n")

	return b.String()
}
