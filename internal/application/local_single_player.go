package application

import (
	"fmt"
	"google.golang.org/protobuf/proto"
	"math/rand"
	"snake-game/internal/domain"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// внутренняя клетка поля (удобнее, чем сразу protobuf)
type cell struct {
	x, y int32
}

// локальная одиночная игра
type localSinglePlayerGame struct {
	cfg      *domain.GameConfig
	state    *domain.GameState
	playerID int32

	stateOut chan<- *domain.GameState

	width, height int32

	snake   []cell
	dir     domain.Direction
	nextDir domain.Direction
	foods   []cell

	gameName   string
	playerName string

	app   *tview.Application
	field *tview.TextView
	info  *tview.TextView

	rng  *rand.Rand
	quit chan struct{}
}

type PlayerInput struct {
	PlayerID  int32
	Direction domain.Direction
}

var globalInputChan chan PlayerInput

// внешняя точка входа для main.go
func RunLocalSinglePlayer(
	cfg *domain.GameConfig,
	playerName, gameName string,
	pType domain.PlayerType,
	stateOut chan<- *domain.GameState,
) error {
	g := newLocalSinglePlayerGame(cfg, playerName, gameName, pType, stateOut)
	return g.run()
}

// --- инициализация ---

func newLocalSinglePlayerGame(
	cfg *domain.GameConfig,
	playerName string,
	gameName string,
	pType domain.PlayerType,
	stateOut chan<- *domain.GameState,
) *localSinglePlayerGame {
	width := cfg.GetWidth()
	height := cfg.GetHeight()
	if width <= 0 {
		width = 40
	}
	if height <= 0 {
		height = 25
	}

	playerID := int32(1)

	nameCopy := playerName
	idCopy := playerID
	role := domain.NodeRole_MASTER
	ptCopy := pType
	score := int32(0)

	player := &domain.GamePlayer{
		Name:  &nameCopy,
		Id:    &idCopy,
		Role:  &role,
		Type:  &ptCopy,
		Score: &score,
	}

	players := &domain.GamePlayers{
		Players: []*domain.GamePlayer{player},
	}

	stateOrder := int32(0)

	// GameState строго по protobuf: только state_order, snakes, foods, players
	state := &domain.GameState{
		StateOrder: &stateOrder,
		Players:    players,
	}
	globalInputChan = make(chan PlayerInput, 64)

	g := &localSinglePlayerGame{
		cfg:        cfg,
		state:      state,
		playerID:   playerID,
		width:      width,
		height:     height,
		gameName:   gameName,
		stateOut:   stateOut,
		playerName: playerName,
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
		quit:       make(chan struct{}),
	}

	g.initSnake()
	g.initFood()

	return g
}

func (g *localSinglePlayerGame) initSnake() {
	head := cell{
		x: g.width / 2,
		y: g.height / 2,
	}
	const initialLen = 4

	g.snake = make([]cell, initialLen)
	for i := 0; i < initialLen; i++ {
		g.snake[i] = cell{
			x: head.x - int32(i),
			y: head.y,
		}
	}

	g.dir = domain.Direction_RIGHT
	g.nextDir = g.dir

	g.syncSnakeProto()
}

func (g *localSinglePlayerGame) initFood() {
	g.foods = nil
	g.ensureFood()
	g.syncFoodProto()
}

// --- синхронизация с protobuf-моделями ---

// кодируем внутренний слайс клеток в GameState_Snake
func (g *localSinglePlayerGame) syncSnakeProto() {
	if len(g.snake) == 0 {
		g.state.Snakes = nil
		return
	}

	points := make([]*domain.GameState_Coord, len(g.snake))

	// первая точка — абсолютные координаты головы змеи
	x0 := g.snake[0].x
	y0 := g.snake[0].y
	points[0] = &domain.GameState_Coord{
		X: &x0,
		Y: &y0,
	}

	// остальные — относительные смещения между соседними клетками
	for i := 1; i < len(g.snake); i++ {
		dx := g.snake[i].x - g.snake[i-1].x
		dy := g.snake[i].y - g.snake[i-1].y
		px := dx
		py := dy
		points[i] = &domain.GameState_Coord{
			X: &px,
			Y: &py,
		}
	}

	pid := g.playerID
	state := domain.GameState_Snake_ALIVE
	dir := g.dir

	snake := &domain.GameState_Snake{
		PlayerId:      &pid,
		Points:        points,
		State:         &state,
		HeadDirection: &dir,
	}

	g.state.Snakes = []*domain.GameState_Snake{snake}
}

func (g *localSinglePlayerGame) syncFoodProto() {
	foods := make([]*domain.GameState_Coord, len(g.foods))
	for i, c := range g.foods {
		x := c.x
		y := c.y
		foods[i] = &domain.GameState_Coord{
			X: &x,
			Y: &y,
		}
	}
	g.state.Foods = foods
}

// --- логика еды ---

func (g *localSinglePlayerGame) ensureFood() {
	// простая оценка: food_static + 1
	need := int(g.cfg.GetFoodStatic() + 1)
	if need < 0 {
		need = 0
	}

	occupied := make(map[cell]struct{}, len(g.snake)+len(g.foods))
	for _, c := range g.snake {
		occupied[c] = struct{}{}
	}
	for _, c := range g.foods {
		occupied[c] = struct{}{}
	}

	maxTries := int(g.width*g.height) * 2
	for len(g.foods) < need && maxTries > 0 {
		maxTries--

		x := int32(g.rng.Intn(int(g.width)))
		y := int32(g.rng.Intn(int(g.height)))
		c := cell{x: x, y: y}
		if _, exists := occupied[c]; exists {
			continue
		}
		g.foods = append(g.foods, c)
		occupied[c] = struct{}{}
	}
}

// --- запуск UI ---

func (g *localSinglePlayerGame) run() error {
	g.app = tview.NewApplication()

	g.field = tview.NewTextView().
		SetDynamicColors(false).
		SetScrollable(false)
	g.field.SetBorder(true).SetTitle(" Игровое поле ")

	g.info = tview.NewTextView().
		SetDynamicColors(true)
	g.info.SetBorder(true).SetTitle(" Информация ")

	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(g.info, 3, 0, false).
		AddItem(g.field, 0, 1, true)

	g.app.SetRoot(root, true)
	g.app.SetInputCapture(g.handleKey)

	// ПЕРВЫЙ КАДР: рисуем синхронно, БЕЗ QueueUpdateDraw
	g.render()

	// игровой цикл — в отдельной горутине
	go g.loop()

	return g.app.Run()
}

// --- управление с клавиатуры ---

func (g *localSinglePlayerGame) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyEsc:
		g.stop()
		return nil
	case tcell.KeyUp:
		g.setNextDir(domain.Direction_UP)
		return nil
	case tcell.KeyDown:
		g.setNextDir(domain.Direction_DOWN)
		return nil
	case tcell.KeyLeft:
		g.setNextDir(domain.Direction_LEFT)
		return nil
	case tcell.KeyRight:
		g.setNextDir(domain.Direction_RIGHT)
		return nil
	}

	switch ev.Rune() {
	case 'w', 'W':
		g.setNextDir(domain.Direction_UP)
		return nil
	case 's', 'S':
		g.setNextDir(domain.Direction_DOWN)
		return nil
	case 'a', 'A':
		g.setNextDir(domain.Direction_LEFT)
		return nil
	case 'd', 'D':
		g.setNextDir(domain.Direction_RIGHT)
		return nil
	case 'q', 'Q':
		g.stop()
		return nil
	}

	return ev
}

func (g *localSinglePlayerGame) setNextDir(d domain.Direction) {
	// не даём развернуться на 180° за один ход
	if (g.dir == domain.Direction_UP && d == domain.Direction_DOWN) ||
		(g.dir == domain.Direction_DOWN && d == domain.Direction_UP) ||
		(g.dir == domain.Direction_LEFT && d == domain.Direction_RIGHT) ||
		(g.dir == domain.Direction_RIGHT && d == domain.Direction_LEFT) {
		return
	}
	g.nextDir = d
}

func (g *localSinglePlayerGame) stop() {
	select {
	case <-g.quit:
		// уже закрыто
	default:
		close(g.quit)
	}
	g.app.Stop()
}

// --- игровой цикл ---

func (g *localSinglePlayerGame) loop() {
	delay := time.Duration(g.cfg.GetStateDelayMs()) * time.Millisecond
	if delay <= 0 {
		delay = 200 * time.Millisecond
	}

	ticker := time.NewTicker(delay)
	defer ticker.Stop()

	for {
		select {
		case <-g.quit:
			return
		case <-ticker.C:
			g.step()
		}
	}
}

func (g *localSinglePlayerGame) dirDelta() (dx, dy int32) {
	switch g.dir {
	case domain.Direction_LEFT:
		return -1, 0
	case domain.Direction_RIGHT:
		return 1, 0
	case domain.Direction_UP:
		return 0, -1
	case domain.Direction_DOWN:
		return 0, 1
	default:
		return 1, 0
	}
}

func (g *localSinglePlayerGame) step() {
	// применяем направление, набранное с клавиатуры
	g.dir = g.nextDir

	if len(g.snake) == 0 {
		return
	}

	head := g.snake[0]
	dx, dy := g.dirDelta()

	// поле — тор
	nx := (head.x + dx + g.width) % g.width
	ny := (head.y + dy + g.height) % g.height
	newHead := cell{x: nx, y: ny}

	// столкновение с самим собой
	for _, c := range g.snake {
		if c == newHead {
			g.gameOver()
			return
		}
	}

	// проверяем еду
	ate := false
	newFoods := make([]cell, 0, len(g.foods))
	for _, f := range g.foods {
		if f == newHead {
			ate = true
		} else {
			newFoods = append(newFoods, f)
		}
	}
	g.foods = newFoods

	// двигаем змейку: новая голова в начало
	g.snake = append([]cell{newHead}, g.snake...)
	if !ate {
		// обычный ход — хвост укорачиваем
		g.snake = g.snake[:len(g.snake)-1]
	} else {
		// съели еду: увеличиваем счёт, генерируем новую
		players := g.state.GetPlayers().GetPlayers()
		if len(players) > 0 && players[0] != nil && players[0].Score != nil {
			*players[0].Score++
		}
		g.ensureFood()
	}

	// увеличиваем state_order
	if g.state.StateOrder == nil {
		v := int32(0)
		g.state.StateOrder = &v
	}
	*g.state.StateOrder++

	// синхронизируем protobuf-состояние
	g.syncSnakeProto()
	g.syncFoodProto()

	// Отдаём снапшот состояния серверу.
	if g.stateOut != nil && g.state != nil {
		snapshot := proto.Clone(g.state).(*domain.GameState)
		select {
		case g.stateOut <- snapshot:
		default:
			// канал заполнен — просто пропускаем этот кадр
		}
	}

	g.redraw()
}

func (g *localSinglePlayerGame) gameOver() {
	g.app.QueueUpdateDraw(func() {
		g.info.SetText("[red::b]Игра окончена[/::-]  (Esc или Q — выход)")
	})
	g.stop()
}

// --- отрисовка ---

// render — «голая» отрисовка, без QueueUpdateDraw.
// Можно вызывать до app.Run() или изнутри QueueUpdateDraw.
func (g *localSinglePlayerGame) render() {
	// панель информации
	score := 0
	displayName := g.playerName
	if players := g.state.GetPlayers().GetPlayers(); len(players) > 0 && players[0] != nil {
		score = int(players[0].GetScore())
		if players[0].GetName() != "" {
			displayName = players[0].GetName()
		}
	}

	title := g.gameName
	if title == "" {
		title = "local game"
	}

	g.info.SetText(fmt.Sprintf(
		"Игра: [white]%s[-]  |  Игрок: [yellow]%s[-]  |  Счёт: [green]%d[-]  |  Размер поля: %dx%d  (WASD/стрелки — управление, Esc/Q — выход)",
		title, displayName, score, g.width, g.height,
	))

	// карта символов для поля
	cellsRunes := make(map[cell]rune, len(g.snake)+len(g.foods))
	for i, c := range g.snake {
		r := 'o'
		if i == 0 {
			r = 'O' // голова
		}
		cellsRunes[c] = r
	}
	for _, f := range g.foods {
		if _, exists := cellsRunes[f]; !exists {
			cellsRunes[f] = '*'
		}
	}

	// собираем строку для TextView
	buf := make([]byte, 0, int((g.width+1)*g.height))
	for y := int32(0); y < g.height; y++ {
		for x := int32(0); x < g.width; x++ {
			ch := ' '
			if r, ok := cellsRunes[cell{x: x, y: y}]; ok {
				ch = r
			}
			buf = append(buf, byte(ch))
		}
		buf = append(buf, '\n')
	}
	g.field.SetText(string(buf))
}

// redraw — безопасно дергаем из других горутин ПОСЛЕ запуска app.Run().
func (g *localSinglePlayerGame) redraw() {
	g.app.QueueUpdateDraw(func() {
		g.render()
	})
}
