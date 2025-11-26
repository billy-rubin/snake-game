package application

type Game struct{}

func NewGame() *Game {
	return &Game{}
}

func (game *Game) Start() error {
	return nil
}
