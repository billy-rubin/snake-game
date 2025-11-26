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
	// Создание новой игры. Вызывается при подтверждении формы "Создать игру".
	CreateGame func(
		cfg *domain.GameConfig,
		playerName string,
		gameName string,
		pType domain.PlayerType,
	) error

	// Подключение к существующей игре.
	JoinGame func(
		ann *domain.GameAnnouncement,
		playerName string,
		requestedRole domain.NodeRole,
		pType domain.PlayerType,
	) error

	// Выход из приложения.
	Exit func()
}

type Menu struct {
	app       *tview.Application
	pages     *tview.Pages
	callbacks Callbacks

	// основной контрол формы главного меню — сюда ставим фокус
	mainForm *tview.Form

	// Список игр для экрана "Подключиться"
	games        []*domain.GameAnnouncement
	gamesTable   *tview.Table
	selectedGame int // индекс в m.games, -1 если ничего
}

const (
	pageMain   = "main"
	pageCreate = "create"
	pageJoin   = "join"
	pageError  = "error"
)

// NewMenu создаёт меню, но не запускает event-loop.
func NewMenu(cb Callbacks) *Menu {
	m := &Menu{
		app:          tview.NewApplication(),
		pages:        tview.NewPages(),
		callbacks:    cb,
		selectedGame: -1,
	}

	// Включаем поддержку мыши, чтобы по клику по кнопкам тоже работало
	m.app.EnableMouse(true)

	mainPage := m.buildMainPage()
	m.pages.AddPage(pageMain, mainPage, true, true)

	// ВАЖНО: фокус ставим именно на форму, а не на Flex/страницу
	m.app.SetRoot(m.pages, true).SetFocus(m.mainForm)

	return m
}

// Run запускает UI и блокирует до выхода.
func (m *Menu) Run() error {
	return m.app.Run()
}

// buildMainPage строит главный экран и запоминает форму в m.mainForm.
func (m *Menu) buildMainPage() tview.Primitive {
	form := tview.NewForm().
		AddButton("Создать игру", func() {
			m.showCreateGamePage()
		}).
		AddButton("Подключиться к игре", func() {
			m.showJoinGamePage()
		}).
		AddButton("Выход", func() {
			if m.callbacks.Exit != nil {
				m.callbacks.Exit()
			}
			m.app.Stop()
		})

	form.SetBorder(true).
		SetTitle(" Змейка по сети ").
		SetTitleAlign(tview.AlignCenter)

	// ESC с главного меню — тоже выход
	form.SetCancelFunc(func() {
		if m.callbacks.Exit != nil {
			m.callbacks.Exit()
		}
		m.app.Stop()
	})

	// Сохраняем форму как "контроллер" главного меню
	m.mainForm = form

	// Центрируем форму по экрану.
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

	return flex
}

func (m *Menu) backToMain() {
	m.pages.SwitchToPage(pageMain)
	// При возврате в главное меню снова ставим фокус на форму
	if m.mainForm != nil {
		m.app.SetFocus(m.mainForm)
	}
}

func (m *Menu) showCreateGamePage() {
	// Значения по умолчанию из protobuf-констант.
	width := domain.Default_GameConfig_Width
	height := domain.Default_GameConfig_Height
	foodStatic := domain.Default_GameConfig_FoodStatic
	stateDelay := domain.Default_GameConfig_StateDelayMs

	gameName := "my game"
	playerName := "player"
	playerType := domain.PlayerType_HUMAN

	form := tview.NewForm().
		AddInputField("Имя игры", gameName, 30, nil, func(text string) {
			gameName = text
		}).
		AddInputField("Имя игрока", playerName, 30, nil, func(text string) {
			playerName = text
		}).
		AddDropDown("Тип игрока", []string{"Человек", "Робот"}, 0, func(option string, index int) {
			if index == 1 {
				playerType = domain.PlayerType_ROBOT
			} else {
				playerType = domain.PlayerType_HUMAN
			}
		}).
		AddInputField("Ширина поля", strconv.Itoa(int(width)), 4,
			func(textToCheck string, lastChar rune) bool {
				_, err := strconv.Atoi(textToCheck)
				return err == nil || textToCheck == ""
			},
			func(text string) {
				if v, err := strconv.Atoi(text); err == nil {
					width = int32(v)
				}
			},
		).
		AddInputField("Высота поля", strconv.Itoa(int(height)), 4,
			func(textToCheck string, lastChar rune) bool {
				_, err := strconv.Atoi(textToCheck)
				return err == nil || textToCheck == ""
			},
			func(text string) {
				if v, err := strconv.Atoi(text); err == nil {
					height = int32(v)
				}
			},
		).
		AddInputField("Food static", strconv.Itoa(int(foodStatic)), 4,
			func(textToCheck string, lastChar rune) bool {
				_, err := strconv.Atoi(textToCheck)
				return err == nil || textToCheck == ""
			},
			func(text string) {
				if v, err := strconv.Atoi(text); err == nil {
					foodStatic = int32(v)
				}
			},
		).
		AddInputField("State delay, ms", strconv.Itoa(int(stateDelay)), 5,
			func(textToCheck string, lastChar rune) bool {
				_, err := strconv.Atoi(textToCheck)
				return err == nil || textToCheck == ""
			},
			func(text string) {
				if v, err := strconv.Atoi(text); err == nil {
					stateDelay = int32(v)
				}
			},
		).
		AddButton("Начать игру", func() {
			if gameName == "" {
				m.showError("Имя игры не может быть пустым")
				return
			}
			if playerName == "" {
				m.showError("Имя игрока не может быть пустым")
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

			if m.callbacks.CreateGame == nil {
				m.showError("CreateGame callback не реализован")
				return
			}
			if err := m.callbacks.CreateGame(cfg, playerName, gameName, playerType); err != nil {
				m.showError(fmt.Sprintf("Ошибка при создании игры: %v", err))
				return
			}

			// Здесь обычно переключаемся на экран самой игры; меню просто останавливает UI.
			m.app.Stop()
		}).
		AddButton("Назад", func() {
			m.backToMain()
		})

	form.SetBorder(true).
		SetTitle(" Создать новую игру ").
		SetTitleAlign(tview.AlignCenter)

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

	if m.pages.HasPage(pageCreate) {
		m.pages.RemovePage(pageCreate)
	}
	m.pages.AddPage(pageCreate, flex, true, true)
	m.pages.SwitchToPage(pageCreate)

	// ВАЖНО: фокус ставим на форму создания игры
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
				// Просто закрываем модалку, остальные страницы остаются как были.
				m.pages.RemovePage(pageError)
			})

		// Если страница ошибки уже есть — удаляем, чтобы не плодить копии.
		if m.pages.HasPage(pageError) {
			m.pages.RemovePage(pageError)
		}

		// Добавляем как модальное окно поверх текущего UI.
		m.pages.AddPage(pageError, modal, true, true)
	})
}

func (m *Menu) showJoinGamePage() {
	if m.gamesTable == nil {
		m.gamesTable = m.buildGamesTable()
	}
	m.selectedGame = -1
	m.refreshGamesTable()

	// Поля формы Join.
	playerName := "player"
	playerType := domain.PlayerType_HUMAN
	requestedRole := domain.NodeRole_NORMAL // NORMAL (играть) или VIEWER (просмотр)

	form := tview.NewForm().
		AddInputField("Имя игрока", playerName, 30, nil, func(text string) {
			playerName = text
		}).
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
				// Если явно не выбрали, берём строку выделения в таблице.
				row, _ := m.gamesTable.GetSelection()
				if row <= 0 || row-1 >= len(m.games) {
					m.showError("Не выбрана игра для подключения")
					return
				}
				idx = row - 1
			}

			ann := m.games[idx]
			if ann == nil {
				m.showError("Внутренняя ошибка: пустой GameAnnouncement")
				return
			}

			// Если пользователь хочет играть, а CanJoin=false, блокируем.
			if requestedRole == domain.NodeRole_NORMAL && !ann.GetCanJoin() {
				m.showError("К этой игре нельзя присоединиться как игрок (нет места на поле). Попробуйте режим 'Только просмотр'.")
				return
			}

			if m.callbacks.JoinGame == nil {
				m.showError("JoinGame callback не реализован")
				return
			}
			if err := m.callbacks.JoinGame(ann, playerName, requestedRole, playerType); err != nil {
				m.showError(fmt.Sprintf("Ошибка при подключении к игре: %v", err))
				return
			}

			m.app.Stop()
		}).
		AddButton("Назад", func() {
			m.backToMain()
		})

	form.SetBorder(true).
		SetTitle(" Подключиться к игре ").
		SetTitleAlign(tview.AlignCenter)

	// Макет: слева таблица игр, справа форма.
	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(
			tview.NewFlex().
				SetDirection(tview.FlexColumn).
				AddItem(m.gamesTable, 0, 2, true).
				AddItem(form, 0, 1, false),
			0, 1, true,
		)

	if m.pages.HasPage(pageJoin) {
		m.pages.RemovePage(pageJoin)
	}
	m.pages.AddPage(pageJoin, root, true, true)
	m.pages.SwitchToPage(pageJoin)

	// При входе в экран подключения логично поставить фокус на таблицу игр,
	// чтобы стрелками выбирать, а дальше Tab — к форме.
	m.app.SetFocus(m.gamesTable)
}

// buildGamesTable создаёт таблицу, но не наполняет её данными.
func (m *Menu) buildGamesTable() *tview.Table {
	table := tview.NewTable().
		SetSelectable(true, false).
		SetBorders(false)

	// Заголовок
	header := []string{"#", "Имя игры", "CanJoin", "Игроки", "Размер", "Food", "Delay, ms"}
	for col, title := range header {
		cell := tview.NewTableCell(title).
			SetAlign(tview.AlignCenter).
			SetSelectable(false).
			SetAttributes(tcell.AttrBold)
		table.SetCell(0, col, cell)
	}

	table.SetSelectedFunc(func(row, column int) {
		// row 0 — заголовок
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

// refreshGamesTable перерисовывает таблицу игр по m.games.
func (m *Menu) refreshGamesTable() {
	if m.gamesTable == nil {
		return
	}

	// Очистить все строки, кроме заголовка.
	rowCount := m.gamesTable.GetRowCount()
	for r := 1; r < rowCount; r++ {
		m.gamesTable.RemoveRow(1) // постоянно удаляем вторую строку
	}

	for i, ann := range m.games {
		row := i + 1

		cfg := ann.GetConfig()
		players := ann.GetPlayers()
		playersCount := 0
		if players != nil {
			playersCount = len(players.GetPlayers())
		}

		width := cfg.GetWidth()
		height := cfg.GetHeight()
		food := cfg.GetFoodStatic()
		delay := cfg.GetStateDelayMs()

		values := []string{
			strconv.Itoa(i + 1),
			ann.GetGameName(),
			fmt.Sprintf("%v", ann.GetCanJoin()),
			strconv.Itoa(playersCount),
			fmt.Sprintf("%dx%d", width, height),
			strconv.Itoa(int(food)),
			strconv.Itoa(int(delay)),
		}

		for c, v := range values {
			cell := tview.NewTableCell(v).
				SetAlign(tview.AlignLeft)
			m.gamesTable.SetCell(row, c, cell)
		}
	}
}

// SetGames обновляет список игр в меню "Подключиться к игре".
// Можно вызывать из любого горути на фоне; обновление будет перенесено в UI-поток.
func (m *Menu) SetGames(games []*domain.GameAnnouncement) {
	m.games = games
	if m.gamesTable == nil {
		return
	}
	// Безопасное обновление из другого goroutine.
	m.app.QueueUpdateDraw(func() {
		m.refreshGamesTable()
	})
}
