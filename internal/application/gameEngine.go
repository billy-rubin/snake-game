package application

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"snake-game/internal/domain"
)

// GameEngine — оркестратор игры на стороне MASTER.
// Он хранит авторитетное состояние (GameState), собирает ходы (Steer) и вызывает ApplyTick.
type GameEngine struct {
	cfg   *domain.GameConfig
	state *domain.GameState

	// Буфер ходов: playerID -> Direction.
	// Очищается после каждого тика.
	steers   map[int32]domain.Direction
	steersMu sync.Mutex

	// Канал, в который мы пушим обновленный стейт для рассылки сервером.
	stateOut chan<- *domain.GameState
	stopCh   chan struct{}

	rng *rand.Rand
	log *log.Logger
}

func NewGameEngine(
	cfg *domain.GameConfig,
	stateOut chan<- *domain.GameState,
	logger *log.Logger,
) *GameEngine {
	if logger == nil {
		logger = log.Default()
	}

	// Инициализируем пустое состояние
	state := &domain.GameState{
		StateOrder: proto.Int32(0),
		Players:    &domain.GamePlayers{},
		Snakes:     []*domain.GameState_Snake{},
		Foods:      []*domain.GameState_Coord{},
	}

	return &GameEngine{
		cfg:      cfg,
		state:    state,
		steers:   make(map[int32]domain.Direction),
		stateOut: stateOut,
		stopCh:   make(chan struct{}),
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
		log:      logger,
	}
}

// AddPlayer добавляет игрока в список. Если роль NORMAL/MASTER — пытается заспаунить змею.
func (e *GameEngine) AddPlayer(p *domain.GamePlayer) error {
	e.steersMu.Lock()
	defer e.steersMu.Unlock()

	// 1. Добавляем в список Players
	if e.state.Players == nil {
		e.state.Players = &domain.GamePlayers{}
	}
	// Проверяем дубликаты (на всякий случай)
	exists := false
	for _, existing := range e.state.Players.Players {
		if existing.GetId() == p.GetId() {
			exists = true
			break
		}
	}
	if !exists {
		e.state.Players.Players = append(e.state.Players.Players, p)
	}

	// 2. Если нужно, спауним змею
	if p.GetRole() == domain.NodeRole_NORMAL || p.GetRole() == domain.NodeRole_MASTER {
		res, err := domain.SpawnNewSnake(e.cfg, e.state, p.GetId(), e.rng)
		if err != nil {
			// Если места нет, меняем роль на VIEWER
			viewer := domain.NodeRole_VIEWER
			p.Role = &viewer
			return fmt.Errorf("cannot spawn snake for player %d: %w", p.GetId(), err)
		}
		e.log.Printf("Spawned snake for player %d at head=%v tail=%v", p.GetId(), res.Head, res.Tail)
	}

	return nil
}

// RemovePlayer переводит игрока в статус зрителя и превращает змею в зомби.
func (e *GameEngine) RemovePlayer(playerID int32) {
	e.steersMu.Lock()
	defer e.steersMu.Unlock()

	// 1. Обновляем роль игрока в списке (не удаляем совсем, чтобы видеть счет)
	for _, p := range e.state.Players.Players {
		if p.GetId() == playerID {
			viewer := domain.NodeRole_VIEWER
			p.Role = &viewer
			break
		}
	}

	// 2. Находим змею и делаем её зомби
	// (domain.SnakeByPlayerID ищет змею в списке)
	for _, s := range e.state.Snakes {
		if s.GetPlayerId() == playerID {
			zombie := domain.GameState_Snake_ZOMBIE
			s.State = &zombie
			// Зомби продолжает движение в том же направлении,
			// steer-команды для него больше не приходят (игрок отключен).
			break
		}
	}

	e.log.Printf("Player %d removed (became ZOMBIE/VIEWER)", playerID)
}

// ApplySteer сохраняет намерение повернуть. Вызывается из GameServer при получении SteerMsg.
func (e *GameEngine) ApplySteer(playerID int32, dir domain.Direction) {
	e.steersMu.Lock()
	defer e.steersMu.Unlock()

	// Важно: мы не применяем поворот сразу, а складываем в буфер до тика.
	// Здесь можно добавить валидацию (нельзя 180 градусов), но ApplyTick в domain тоже это проверяет.
	e.steers[playerID] = dir
}

// Run запускает игровой цикл (Tick). Блокирует поток.
func (e *GameEngine) Run() {
	delayMs := e.cfg.GetStateDelayMs()
	if delayMs < 10 {
		delayMs = 100
	}
	ticker := time.NewTicker(time.Duration(delayMs) * time.Millisecond)
	defer ticker.Stop()

	// Первый стейт отправляем сразу
	e.broadcastState()

	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.tick()
		}
	}
}

func (e *GameEngine) Stop() {
	close(e.stopCh)
}

func (e *GameEngine) tick() {
	e.steersMu.Lock()
	defer e.steersMu.Unlock()

	// Вызываем чистую доменную логику
	result, err := domain.ApplyTick(e.cfg, e.state, e.steers, e.rng)
	if err != nil {
		e.log.Printf("Error in ApplyTick: %v", err)
		return
	}

	// Очищаем буфер команд (или можно оставлять, если логика требует continuous input,
	// но в Змейке обычно команда действует 1 раз или сохраняет направление state-ом).
	// В данном случае ApplyTick сам берет текущее направление, если steer нет.
	// Так что map steers нужно чистить, чтобы не "залипали" повороты.
	e.steers = make(map[int32]domain.Direction)

	// Увеличиваем номер состояния
	if e.state.StateOrder == nil {
		zero := int32(0)
		e.state.StateOrder = &zero
	}
	*e.state.StateOrder++

	// Логируем важные события
	if len(result.DeadPlayers) > 0 {
		e.log.Printf("Tick %d: Players died: %v", e.state.GetStateOrder(), result.DeadPlayers)
	}

	// Отправляем обновление
	e.broadcastState()
}

func (e *GameEngine) broadcastState() {
	// Клонируем стейт перед отправкой в канал, чтобы не было гонок
	cloned := proto.Clone(e.state).(*domain.GameState)
	select {
	case e.stateOut <- cloned:
	default:
		// Если канал полон, пропускаем (клиенты получат следующий стейт)
	}
}
