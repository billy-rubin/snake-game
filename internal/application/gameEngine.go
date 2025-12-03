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

type GameEngine struct {
	cfg   *domain.GameConfig
	state *domain.GameState

	// Буфер ходов: playerID -> Direction.
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

func (e *GameEngine) AddPlayer(p *domain.GamePlayer) error {
	e.steersMu.Lock()
	defer e.steersMu.Unlock()

	if e.state.Players == nil {
		e.state.Players = &domain.GamePlayers{}
	}
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

	if p.GetRole() == domain.NodeRole_NORMAL || p.GetRole() == domain.NodeRole_MASTER {
		res, err := domain.SpawnNewSnake(e.cfg, e.state, p.GetId(), e.rng)
		if err != nil {
			viewer := domain.NodeRole_VIEWER
			p.Role = &viewer
			return fmt.Errorf("cannot spawn snake for player %d: %w", p.GetId(), err)
		}
		e.log.Printf("Spawned snake for player %d at head=%v tail=%v", p.GetId(), res.Head, res.Tail)
	}

	return nil
}

func (e *GameEngine) RemovePlayer(playerID int32) {
	e.steersMu.Lock()
	defer e.steersMu.Unlock()

	for _, p := range e.state.Players.Players {
		if p.GetId() == playerID {
			viewer := domain.NodeRole_VIEWER
			p.Role = &viewer
			break
		}
	}

	for _, s := range e.state.Snakes {
		if s.GetPlayerId() == playerID {
			zombie := domain.GameState_Snake_ZOMBIE
			s.State = &zombie
			break
		}
	}

	e.log.Printf("Player %d removed (became ZOMBIE/VIEWER)", playerID)
}

func (e *GameEngine) ApplySteer(playerID int32, dir domain.Direction) {
	e.steersMu.Lock()
	defer e.steersMu.Unlock()
	e.steers[playerID] = dir
}

func (e *GameEngine) Run() {
	delayMs := e.cfg.GetStateDelayMs()
	if delayMs < 10 {
		delayMs = 100
	}
	ticker := time.NewTicker(time.Duration(delayMs) * time.Millisecond)
	defer ticker.Stop()

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

	result, err := domain.ApplyTick(e.cfg, e.state, e.steers, e.rng)
	if err != nil {
		e.log.Printf("Error in ApplyTick: %v", err)
		return
	}
	e.steers = make(map[int32]domain.Direction)

	if e.state.StateOrder == nil {
		zero := int32(0)
		e.state.StateOrder = &zero
	}
	*e.state.StateOrder++

	if len(result.DeadPlayers) > 0 {
		e.log.Printf("Tick %d: Players died: %v", e.state.GetStateOrder(), result.DeadPlayers)
	}

	e.broadcastState()
}

func (e *GameEngine) broadcastState() {
	cloned := proto.Clone(e.state).(*domain.GameState)
	select {
	case e.stateOut <- cloned:
	default:
		// skip
	}
}
