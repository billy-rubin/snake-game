package snake_game

func main() {
	err, game := application.NewGame()
	if err != nil {
		panic(err)
	}
	err := game.Start()
	if err != nil {
		panic(err)
	}
}
