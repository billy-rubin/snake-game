// internal/domain/random.go
package domain

// Random — минимальный интерфейс случайного генератора, чтобы
// можно было легко подменять его в тестах.
type Random interface {
	Intn(n int) int   // как math/rand.Rand.Intn
	Float64() float64 // как math/rand.Rand.Float64
}
