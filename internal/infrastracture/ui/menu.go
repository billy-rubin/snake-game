package ui

import (
	"fmt"
	"snake-game/internal/domain"
	"strconv"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Callbacks — точки входа в application-слой.
type Callbacks struct {
	CreateGame func(cfg *domain.GameConfig, playerName string, gameName string, pType domain.PlayerType) error
	JoinGame   func(ann *domain.GameAnnouncement, playerName string, requestedRole domain.NodeRole, pType domain.PlayerType) error
	Exit       func()
}

type Menu struct {
	app          *tview.Application
	pages        *tview.Pages
	callbacks    Callbacks
	mainForm     *tview.Form
	games        []*domain.GameAnnouncement
	gamesTable   *tview.Table
	selectedGame int
	appRunning   bool
}

const (
	pageMain   = "main"
	pageCreate = "create"
	pageJoin   = "join"
	pageError  = "error"
)

func NewMenu(cb Callbacks) *Menu {
	m := &Menu{
		app:          tview.NewApplication(),
		pages:        tview.NewPages(),
		callbacks:    cb,
		selectedGame: -1,
	}
	m.app.EnableMouse(true)

	mainPage := m.buildMainPage()
	m.pages.AddPage(pageMain, mainPage, true, true)
	m.app.SetRoot(m.pages, true).SetFocus(m.mainForm)

	return m
}

func (m *Menu) buildMainPage() tview.Primitive {
	form := tview.NewForm().
		AddButton("Создать игру", func() { m.showCreateGamePage() }).
		AddButton("Подключиться к игре", func() { m.showJoinGamePage() }).
		AddButton("Выход", func() {
			if m.callbacks.Exit != nil {
				m.callbacks.Exit()
			}
			m.app.Stop()
		})

	form.SetBorder(true).
		SetTitle(" Змейка по сети ").
		SetTitleAlign(tview.AlignCenter)

	form.SetCancelFunc(func() {
		if m.callbacks.Exit != nil {
			m.callbacks.Exit()
		}
		m.app.Stop()
	})

	m.mainForm = form

	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(
			tview.NewFlex().
				SetDirection(tview.FlexColumn).
				AddItem(nil, 0, 1, false).
				AddItem(form, 50, 1, true).
				AddItem(nil, 0, 1, false),
			0, 1, true,
		).
		AddItem(nil, 0, 1, false)

	flex.SetBackgroundColor(tview.Styles.PrimitiveBackgroundColor)

	return flex
}

func (m *Menu) backToMain() {
	m.pages.SwitchToPage(pageMain)
	if m.mainForm != nil {
		m.app.SetFocus(m.mainForm)
	}
}

func (m *Menu) showCreateGamePage() {
	width := domain.Default_GameConfig_Width
	height := domain.Default_GameConfig_Height
	foodStatic := domain.Default_GameConfig_FoodStatic
	stateDelay := domain.Default_GameConfig_StateDelayMs

	gameName := "my game"
	playerName := "player"
	playerType := domain.PlayerType_HUMAN

	form := tview.NewForm().
		AddInputField("Имя игры", gameName, 30, nil, func(text string) { gameName = text }).
		AddInputField("Имя игрока", playerName, 30, nil, func(text string) { playerName = text }).
		AddDropDown("Тип игрока", []string{"Человек", "Робот"}, 0, func(option string, index int) {
			if index == 1 {
				playerType = domain.PlayerType_ROBOT
			} else {
				playerType = domain.PlayerType_HUMAN
			}
		}).
		AddInputField("Ширина поля", strconv.Itoa(int(width)), 4, nil, func(text string) {
			if v, err := strconv.Atoi(text); err == nil {
				width = int32(v)
			}
		}).
		AddInputField("Высота поля", strconv.Itoa(int(height)), 4, nil, func(text string) {
			if v, err := strconv.Atoi(text); err == nil {
				height = int32(v)
			}
		}).
		AddInputField("Food static", strconv.Itoa(int(foodStatic)), 4, nil, func(text string) {
			if v, err := strconv.Atoi(text); err == nil {
				foodStatic = int32(v)
			}
		}).
		AddInputField("State delay, ms", strconv.Itoa(int(stateDelay)), 5, nil, func(text string) {
			if v, err := strconv.Atoi(text); err == nil {
				stateDelay = int32(v)
			}
		}).
		AddButton("Начать игру", func() {
			if gameName == "" || playerName == "" {
				m.showError("Имя игры и игрока не могут быть пустыми")
				return
			}
			cfg := &domain.GameConfig{
				Width:        &width,
				Height:       &height,
				FoodStatic:   &foodStatic,
				StateDelayMs: &stateDelay,
			}
			if err := domain.ValidateConfig(cfg); err != nil {
				m.showError(fmt.Sprintf("Некорректный конфиг: %v", err))
				return
			}
			if m.callbacks.CreateGame != nil {
				if err := m.callbacks.CreateGame(cfg, playerName, gameName, playerType); err != nil {
					m.showError(err.Error())
					return
				}
			}
			m.app.Stop()
		}).
		AddButton("Назад", func() { m.backToMain() })

	form.SetBorder(true).SetTitle(" Создать новую игру ").SetTitleAlign(tview.AlignCenter)

	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(
			tview.NewFlex().
				SetDirection(tview.FlexColumn).
				AddItem(nil, 0, 1, false).
				AddItem(form, 60, 1, true).
				AddItem(nil, 0, 1, false),
			0, 1, true,
		).
		AddItem(nil, 0, 1, false)

	flex.SetBackgroundColor(tview.Styles.PrimitiveBackgroundColor)

	if m.pages.HasPage(pageCreate) {
		m.pages.RemovePage(pageCreate)
	}
	m.pages.AddPage(pageCreate, flex, true, true)
	m.pages.SwitchToPage(pageCreate)
	m.app.SetFocus(form)
}

func (m *Menu) showError(message string) {
	if message == "" {
		message = "Неизвестная ошибка"
	}
	m.app.QueueUpdateDraw(func() {
		modal := tview.NewModal().
			SetText(message).
			AddButtons([]string{"OK"}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				m.pages.RemovePage(pageError)
			})
		if m.pages.HasPage(pageError) {
			m.pages.RemovePage(pageError)
		}
		m.pages.AddPage(pageError, modal, true, true)
	})
}

func (m *Menu) showJoinGamePage() {
	if m.gamesTable == nil {
		m.gamesTable = m.buildGamesTable()
	}
	m.selectedGame = -1
	m.refreshGamesTable()

	m.gamesTable.SetBorder(true).SetTitle(" Список доступных игр ").SetTitleAlign(tview.AlignCenter)

	playerName := "player"
	playerType := domain.PlayerType_HUMAN
	requestedRole := domain.NodeRole_NORMAL

	form := tview.NewForm().
		AddInputField("Имя игрока", playerName, 30, nil, func(text string) { playerName = text }).
		AddDropDown("Режим", []string{"Играть", "Только просмотр"}, 0, func(option string, index int) {
			if index == 1 {
				requestedRole = domain.NodeRole_VIEWER
			} else {
				requestedRole = domain.NodeRole_NORMAL
			}
		}).
		AddDropDown("Тип игрока", []string{"Человек", "Робот"}, 0, func(option string, index int) {
			if index == 1 {
				playerType = domain.PlayerType_ROBOT
			} else {
				playerType = domain.PlayerType_HUMAN
			}
		}).
		AddButton("Присоединиться", func() {
			if playerName == "" {
				m.showError("Имя игрока не может быть пустым")
				return
			}
			idx := m.selectedGame
			if idx < 0 || idx >= len(m.games) {
				row, _ := m.gamesTable.GetSelection()
				if row <= 0 || row-1 >= len(m.games) {
					m.showError("Не выбрана игра")
					return
				}
				idx = row - 1
			}
			ann := m.games[idx]
			if requestedRole == domain.NodeRole_NORMAL && !ann.GetCanJoin() {
				m.showError("Нет места. Попробуйте режим 'Только просмотр'.")
				return
			}
			if m.callbacks.JoinGame != nil {
				if err := m.callbacks.JoinGame(ann, playerName, requestedRole, playerType); err != nil {
					m.showError(err.Error())
					return
				}
			}
			m.app.Stop()
		}).
		AddButton("Назад", func() { m.backToMain() })

	form.SetBorder(true).SetTitle(" Параметры подключения ").SetTitleAlign(tview.AlignCenter)

	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(m.gamesTable, 0, 1, true).
		AddItem(form, 15, 1, false)

	root.SetBackgroundColor(tview.Styles.PrimitiveBackgroundColor)

	if m.pages.HasPage(pageJoin) {
		m.pages.RemovePage(pageJoin)
	}
	m.pages.AddPage(pageJoin, root, true, true)
	m.pages.SwitchToPage(pageJoin)
	m.app.SetFocus(m.gamesTable)
}

func (m *Menu) buildGamesTable() *tview.Table {
	table := tview.NewTable().SetSelectable(true, false).SetBorders(false)
	header := []string{"#", "Имя игры", "CanJoin", "Игроки", "Размер", "Food", "Delay"}
	for col, title := range header {
		cell := tview.NewTableCell(title).SetAlign(tview.AlignCenter).SetSelectable(false).SetAttributes(tcell.AttrBold)
		table.SetCell(0, col, cell)
	}
	table.SetSelectedFunc(func(row, column int) {
		if row <= 0 {
			return
		}
		idx := row - 1
		if idx < 0 || idx >= len(m.games) {
			return
		}
		m.selectedGame = idx
	})
	return table
}

func (m *Menu) refreshGamesTable() {
	if m.gamesTable == nil {
		return
	}
	rowCount := m.gamesTable.GetRowCount()
	for r := 1; r < rowCount; r++ {
		m.gamesTable.RemoveRow(1)
	}

	for i, ann := range m.games {
		row := i + 1
		cfg := ann.GetConfig()
		plCount := 0
		if ann.GetPlayers() != nil {
			plCount = len(ann.GetPlayers().GetPlayers())
		}

		values := []string{
			strconv.Itoa(i + 1),
			ann.GetGameName(),
			fmt.Sprintf("%v", ann.GetCanJoin()),
			strconv.Itoa(plCount),
			fmt.Sprintf("%dx%d", cfg.GetWidth(), cfg.GetHeight()),
			strconv.Itoa(int(cfg.GetFoodStatic())),
			strconv.Itoa(int(cfg.GetStateDelayMs())),
		}
		for c, v := range values {
			m.gamesTable.SetCell(row, c, tview.NewTableCell(v).SetAlign(tview.AlignLeft))
		}
	}
}

func (m *Menu) Run() error {
	m.appRunning = true
	defer func() { m.appRunning = false }()
	return m.app.Run()
}

func (m *Menu) SetGames(games []*domain.GameAnnouncement) {
	m.games = games
	if m.gamesTable == nil || !m.appRunning {
		return
	}
	m.app.QueueUpdateDraw(func() { m.refreshGamesTable() })
}
