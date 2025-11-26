package main

import (
	"log"

	"snake-game/internal/application"
	"snake-game/internal/domain"
	"snake-game/internal/infrastracture/ui"
)

// что выбрать после меню
type startLocalGameRequest struct {
	cfg        *domain.GameConfig
	playerName string
	gameName   string
	playerType domain.PlayerType
}

func main() {
	var startReq *startLocalGameRequest
	var exitRequested bool

	callbacks := ui.Callbacks{
		CreateGame: func(
			cfg *domain.GameConfig,
			playerName string,
			gameName string,
			pType domain.PlayerType,
		) error {
			// просто сохраняем, что пользователь хочет локальную игру
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
			log.Printf("[MENU] JoinGame пока не реализован (ann=%q, player=%q, role=%v, type=%v)",
				ann.GetGameName(), playerName, requestedRole, pType)
			// сюда позже повесим сетевой клиент
			return nil
		},
		Exit: func() {
			exitRequested = true
		},
	}

	menu := ui.NewMenu(callbacks)

	// Можно добавить демо-игру, чтобы Join-экран не был пустой
	// (как у тебя уже было).
	// menu.SetGames(...)

	if err := menu.Run(); err != nil {
		log.Fatal(err)
	}

	if exitRequested {
		return
	}

	if startReq != nil {
		// Запускаем локальную однопользовательскую игру.
		if err := application.RunLocalSinglePlayer(
			startReq.cfg,
			startReq.playerName,
			startReq.gameName,
			startReq.playerType,
		); err != nil {
			log.Fatal(err)
		}
	}
}
