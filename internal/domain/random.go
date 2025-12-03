// internal/domain/random.go
package domain

type Random interface {
	Intn(n int) int
	Float64() float64
}
