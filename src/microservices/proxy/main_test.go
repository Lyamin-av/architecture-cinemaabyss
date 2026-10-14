package main

import (
	"math/rand/v2"
	"testing"
)

// fixedRand — «случайное» число, которое всегда возвращает одно значение.
func fixedRand(n int) func(int) int { return func(int) int { return n } }

func TestChooseTarget(t *testing.T) {
	gradual := func(pc int) config { return config{GradualMigration: true, MoviesMigrationPc: pc} }

	tests := []struct {
		name string
		path string
		cfg  config
		rnd  int
		want string
	}{
		{"фильмы, 50 %, число 49 → movies", "/api/movies", gradual(50), 49, targetMovies},
		{"фильмы, 50 %, число 50 → монолит", "/api/movies", gradual(50), 50, targetMonolith},
		{"фильмы, 0 % → всегда монолит", "/api/movies", gradual(0), 0, targetMonolith},
		{"фильмы, 100 % → всегда movies", "/api/movies", gradual(100), 99, targetMovies},
		{"флаг выключен → весь домен в movies", "/api/movies", config{GradualMigration: false}, 99, targetMovies},
		{"подпуть фильмов делится так же", "/api/movies/health", gradual(100), 0, targetMovies},
		{"похожий, но чужой путь → монолит", "/api/moviesX", gradual(100), 0, targetMonolith},
		{"события → events", "/api/events/movie", gradual(50), 0, targetEvents},
		{"пользователи → монолит", "/api/users", gradual(100), 0, targetMonolith},
		{"платежи → монолит", "/api/payments", gradual(100), 0, targetMonolith},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chooseTarget(tt.path, tt.cfg, fixedRand(tt.rnd)); got != tt.want {
				t.Errorf("chooseTarget(%q) = %s, ожидали %s", tt.path, got, tt.want)
			}
		})
	}
}

// При 30 % на большом числе запросов доля movies должна быть около 30 %.
func TestMigrationShare(t *testing.T) {
	cfg := config{GradualMigration: true, MoviesMigrationPc: 30}
	const n = 100_000
	movies := 0
	for range n {
		if chooseTarget("/api/movies", cfg, rand.IntN) == targetMovies {
			movies++
		}
	}
	share := float64(movies) / n * 100
	if share < 28 || share > 32 {
		t.Errorf("доля movies = %.1f %%, ожидали около 30 %%", share)
	}
}
