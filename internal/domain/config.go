package domain

import (
	"errors"
	"fmt"
)

const (
	MinWidth      = 10
	MaxWidth      = 100
	MinHeight     = 10
	MaxHeight     = 100
	MinFoodStatic = 0
	MaxFoodStatic = 100
	MinStateDelay = 100
	MaxStateDelay = 3000
)

// BoardSize хранит размеры поля в удобном виде.
type BoardSize struct {
	Width  int32
	Height int32
}

func BoardSizeFromConfig(cfg *GameConfig) BoardSize {
	return BoardSize{
		Width:  cfg.GetWidth(),
		Height: cfg.GetHeight(),
	}
}

var ErrInvalidConfig = errors.New("domain: invalid game config")

// ValidateConfig проверяет, что параметры лежат в допустимых диапазонах
// (как описано в комментариях к GameConfig в proto).
func ValidateConfig(cfg *GameConfig) error {
	if cfg == nil {
		return fmt.Errorf("%w: nil", ErrInvalidConfig)
	}

	w := cfg.GetWidth()
	h := cfg.GetHeight()
	f := cfg.GetFoodStatic()
	d := cfg.GetStateDelayMs()

	if w < MinWidth || w > MaxWidth {
		return fmt.Errorf("%w: width=%d must be in [%d,%d]", ErrInvalidConfig, w, MinWidth, MaxWidth)
	}
	if h < MinHeight || h > MaxHeight {
		return fmt.Errorf("%w: height=%d must be in [%d,%d]", ErrInvalidConfig, h, MinHeight, MaxHeight)
	}
	if f < MinFoodStatic || f > MaxFoodStatic {
		return fmt.Errorf("%w: food_static=%d must be in [%d,%d]", ErrInvalidConfig, f, MinFoodStatic, MaxFoodStatic)
	}
	if d < MinStateDelay || d > MaxStateDelay {
		return fmt.Errorf("%w: state_delay_ms=%d must be in [%d,%d]", ErrInvalidConfig, d, MinStateDelay, MaxStateDelay)
	}
	return nil
}

func int32p(v int32) *int32 { return &v }
func int64p(v int64) *int64 { return &v }
func boolp(v bool) *bool    { return &v }
func stringp(v string) *string {
	return &v
}
func playerTyePtr(t PlayerType) *PlayerType { return &t }
func nodeRolePtr(r NodeRole) *NodeRole      { return &r }
