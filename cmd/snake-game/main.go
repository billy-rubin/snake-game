package main

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/proto"

	"snake-game/internal/application"
	"snake-game/internal/domain"
	"snake-game/internal/infrastracture/ui"
)

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
		CreateGame: func(cfg *domain.GameConfig, playerName string, gameName string, pType domain.PlayerType) error {
			startReq = &startLocalGameRequest{
				cfg:        cfg,
				playerName: playerName,
				gameName:   gameName,
				playerType: pType,
			}
			return nil
		},
		JoinGame: func(ann *domain.GameAnnouncement, playerName string, requestedRole domain.NodeRole, pType domain.PlayerType) error {
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
		runAsMaster(startReq, serverLogger) // <--- Тут изменения внутри

	case haveJoinReq:
		runAsClient(&joinReq, clientLogger)

	default:
	}
}

func runAsMaster(req *startLocalGameRequest, serverLogger *log.Logger) {
	// engineOut - сюда пишет Engine
	engineOut := make(chan *domain.GameState, 32)

	// serverIn - отсюда читает GameServer для рассылки по сети
	serverIn := make(chan *domain.GameState, 32)

	// uiIn - отсюда читает MasterController для отрисовки хосту
	uiIn := make(chan *domain.GameState, 32)

	// Читает из engineOut и пересылает копии в serverIn и uiIn
	go func() {
		defer close(serverIn)
		defer close(uiIn)

		for st := range engineOut {

			stForServer := proto.Clone(st).(*domain.GameState)
			stForUI := proto.Clone(st).(*domain.GameState)

			select {
			case serverIn <- stForServer:
			default:
			}

			select {
			case uiIn <- stForUI:
			default:
			}
		}
	}()

	engine := application.NewGameEngine(req.cfg, engineOut, serverLogger)

	srv, err := application.NewGameServer(
		req.cfg,
		req.gameName,
		req.playerName,
		req.playerType,
		serverLogger,
		serverIn,
		engine,
	)
	if err != nil {
		serverLogger.Fatalf("cannot create GameServer: %v", err)
	}

	masterCtrl := application.NewMasterController(
		req.gameName,
		1,
		req.cfg,
		engine,
		uiIn,
	)

	go engine.Run()

	go func() {
		if err := srv.Run(); err != nil {
			serverLogger.Printf("GameServer stopped: %v", err)
		}
	}()

	if err := masterCtrl.Run(); err != nil {
		serverLogger.Printf("Master UI stopped: %v", err)
	}

	engine.Stop()
	time.Sleep(200 * time.Millisecond)
}

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
		return
	}
	players := ann.GetPlayers().GetPlayers()
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
	ipStr := master.GetIpAddress()
	ip := net.ParseIP(ipStr)
	if ip == nil || ip.IsUnspecified() {
		ip = net.ParseIP("127.0.0.1")
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
		req.requested,
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

	if err := client.RunGame(); err != nil {
		clientLogger.Printf("client game finished with error: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
}
