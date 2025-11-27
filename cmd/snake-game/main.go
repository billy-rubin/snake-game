package main

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"snake-game/internal/application"
	"snake-game/internal/domain"
	"snake-game/internal/infrastracture/ui"
)

// ====== логгер в файлы logs/ ======

func newFileLogger(fileName string, prefix string) (*log.Logger, *os.File, error) {
	if err := os.MkdirAll("logs", 0o755); err != nil {
		return nil, nil, err
	}
	path := filepath.Join("logs", fileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, nil, err
	}
	logger := log.New(f, prefix, log.LstdFlags|log.Lmicroseconds)
	return logger, f, nil
}

func int32Ptr(v int32) *int32 { return &v }

// ====== MAIN ======

type startLocalGameRequest struct {
	cfg        *domain.GameConfig
	playerName string
	gameName   string
	playerType domain.PlayerType
}

func main() {
	serverLogger, serverFile, err := newFileLogger("server.log", "[SERVER] ")
	if err != nil {
		log.Fatalf("cannot create server logger: %v", err)
	}
	defer serverFile.Close()

	clientLogger, clientFile, err := newFileLogger("client.log", "[CLIENT] ")
	if err != nil {
		log.Fatalf("cannot create client logger: %v", err)
	}
	defer clientFile.Close()

	var (
		startReq      *startLocalGameRequest
		exitRequested bool
		joinReq       struct {
			ann        *domain.GameAnnouncement
			playerName string
			playerType domain.PlayerType
			requested  domain.NodeRole
		}
		haveJoinReq bool
	)

	callbacks := ui.Callbacks{
		CreateGame: func(
			cfg *domain.GameConfig,
			playerName string,
			gameName string,
			pType domain.PlayerType,
		) error {
			startReq = &startLocalGameRequest{
				cfg:        cfg,
				playerName: playerName,
				gameName:   gameName,
				playerType: pType,
			}
			return nil
		},
		JoinGame: func(
			ann *domain.GameAnnouncement,
			playerName string,
			requestedRole domain.NodeRole,
			pType domain.PlayerType,
		) error {
			joinReq.ann = ann
			joinReq.playerName = playerName
			joinReq.requested = requestedRole
			joinReq.playerType = pType
			haveJoinReq = true
			return nil
		},
		Exit: func() {
			exitRequested = true
		},
	}

	menu := ui.NewMenu(callbacks)

	// Стартуем Lobby, чтобы список игр в меню "Подключиться" обновлялся автоматически.
	lobby, err := application.NewLobby(clientLogger, func(games []*domain.GameAnnouncement) {
		menu.SetGames(games)
	})
	if err != nil {
		log.Fatalf("cannot start Lobby: %v", err)
	}
	defer lobby.Close()

	if err := menu.Run(); err != nil {
		log.Fatal(err)
	}

	if exitRequested {
		return
	}

	switch {
	case startReq != nil:
		runAsMaster(startReq, serverLogger)

	case haveJoinReq:
		runAsClient(&joinReq, clientLogger)

	default:
		// ничего не выбрано
	}
}

// MASTER: поднимаем GameServer + локальный одиночный движок.
// ВАЖНО: RunLocalSinglePlayer должен принимать stateOut chan<- *domain.GameState
// и на каждом шаге отсылать туда текущее состояние.
func runAsMaster(req *startLocalGameRequest, serverLogger *log.Logger) {
	cfg := req.cfg
	gameName := req.gameName

	// Канал для стриминга состояния от движка к GameServer.
	stateCh := make(chan *domain.GameState, 16)

	srv, err := application.NewGameServer(
		cfg,
		gameName,
		req.playerName,
		req.playerType,
		serverLogger,
		stateCh,
	)
	if err != nil {
		serverLogger.Fatalf("cannot create GameServer: %v", err)
	}

	serverUDP := srv.Addr()
	serverLogger.Printf("GameServer is starting on %s", serverUDP)

	// Запускаем сервер в отдельной горутине.
	go func() {
		if err := srv.Run(); err != nil {
			serverLogger.Printf("GameServer stopped with error: %v", err)
		}
	}()

	// Локальная однопользовательская игра, которая публикует GameState в stateCh.
	if err := application.RunLocalSinglePlayer(
		cfg,
		req.playerName,
		gameName,
		req.playerType,
		stateCh, // новый параметр: канал для GameState
	); err != nil {
		serverLogger.Printf("local single-player game error: %v", err)
	}

	// Закрываем канал, чтобы stateBroadcastLoop завершился.
	close(stateCh)

	time.Sleep(500 * time.Millisecond)
}

// CLIENT: подключаемся к выбранному анонсу и запускаем viewer.
func runAsClient(
	req *struct {
		ann        *domain.GameAnnouncement
		playerName string
		playerType domain.PlayerType
		requested  domain.NodeRole
	},
	clientLogger *log.Logger,
) {
	ann := req.ann
	if ann == nil {
		clientLogger.Printf("no announcement selected")
		return
	}

	players := ann.GetPlayers().GetPlayers()
	if len(players) == 0 {
		clientLogger.Printf("announcement has no players (no MASTER?)")
		return
	}

	var master *domain.GamePlayer
	for _, p := range players {
		if p.GetRole() == domain.NodeRole_MASTER {
			master = p
			break
		}
	}
	if master == nil {
		clientLogger.Printf("no MASTER player in announcement")
		return
	}

	ip := net.ParseIP(master.GetIpAddress())
	if ip == nil {
		clientLogger.Printf("MASTER has invalid IP %q", master.GetIpAddress())
		return
	}
	serverAddr := &net.UDPAddr{
		IP:   ip,
		Port: int(master.GetPort()),
	}

	client, err := application.NewGameClient(
		serverAddr,
		ann.GetGameName(),
		req.playerName,
		req.playerType,
		clientLogger,
	)
	if err != nil {
		clientLogger.Printf("cannot create GameClient: %v", err)
		return
	}

	if err := client.JoinOnce(); err != nil {
		clientLogger.Printf("JoinOnce error: %v", err)
		return
	}
	clientLogger.Printf("JoinOnce succeeded, starting viewer...")

	if err := client.RunViewer(); err != nil {
		clientLogger.Printf("viewer finished with error: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
}
