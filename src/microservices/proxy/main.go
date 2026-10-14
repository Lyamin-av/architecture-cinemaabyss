// Proxy Service (API Gateway) — единая точка входа «Кинобездны» на переходном этапе.
//
// Реализует паттерн Strangler Fig: запросы к /api/movies постепенно
// переводятся из монолита в Movies Service, остальные запросы идут
// в монолит, пока их домены не вынесены.
//
// Настройка — только через переменные окружения (см. loadConfig):
//
//	PORT                      порт прокси (по умолчанию 8000)
//	MONOLITH_URL              адрес монолита
//	MOVIES_SERVICE_URL        адрес Movies Service
//	EVENTS_SERVICE_URL        адрес Events Service
//	GRADUAL_MIGRATION         фиче-флаг постепенной миграции (true/false)
//	MOVIES_MIGRATION_PERCENT  доля запросов /api/movies в Movies Service, 0–100
//
// Поведение флага (как описано в README шаблона):
//   - GRADUAL_MIGRATION=true  — в Movies Service уходит MOVIES_MIGRATION_PERCENT %
//     запросов /api/movies, остальные — в монолит;
//   - GRADUAL_MIGRATION=false — постепенная миграция выключена, весь трафик
//     домена фильмов идёт в Movies Service.
package main

import (
	"encoding/json"
	"log"
	"math/rand/v2"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// config — настройки прокси, прочитанные из переменных окружения.
type config struct {
	Port              string
	MonolithURL       *url.URL
	MoviesURL         *url.URL
	EventsURL         *url.URL
	GradualMigration  bool
	MoviesMigrationPc int // 0–100
}

// Имена целевых сервисов — пишутся в лог и в заголовок ответа X-Routed-To,
// чтобы при проверке было видно, кто обработал запрос.
const (
	targetMonolith = "monolith"
	targetMovies   = "movies-service"
	targetEvents   = "events-service"
)

func main() {
	cfg := loadConfig()

	// Для каждого сервиса — готовый обратный прокси из стандартной библиотеки.
	// Он пересылает метод, путь, заголовки и тело запроса как есть.
	proxies := map[string]*httputil.ReverseProxy{
		targetMonolith: newReverseProxy(cfg.MonolithURL, targetMonolith),
		targetMovies:   newReverseProxy(cfg.MoviesURL, targetMovies),
		targetEvents:   newReverseProxy(cfg.EventsURL, targetEvents),
	}

	mux := http.NewServeMux()

	// Собственная проверка работоспособности прокси (не пересылается).
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": true})
	})

	// Все остальные запросы: выбираем сервис по правилам и пересылаем.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		target := chooseTarget(r.URL.Path, cfg, rand.IntN)
		log.Printf("%s %s -> %s", r.Method, r.URL.RequestURI(), target)
		w.Header().Set("X-Routed-To", target)
		proxies[target].ServeHTTP(w, r)
	})

	log.Printf("Proxy запущен на :%s; монолит=%s, movies=%s, events=%s; GRADUAL_MIGRATION=%t, MOVIES_MIGRATION_PERCENT=%d",
		cfg.Port, cfg.MonolithURL, cfg.MoviesURL, cfg.EventsURL, cfg.GradualMigration, cfg.MoviesMigrationPc)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}

// chooseTarget — вся логика маршрутизации Strangler Fig в одном месте.
// randIntN передаётся параметром, чтобы в тестах подставить предсказуемое
// «случайное» число; в работе это rand.IntN.
func chooseTarget(path string, cfg config, randIntN func(int) int) string {
	switch {
	case hasPrefix(path, "/api/movies"):
		if !cfg.GradualMigration {
			// Миграция завершена: весь домен фильмов — в новый сервис.
			return targetMovies
		}
		// Число от 0 до 99: при проценте 30 в Movies уйдут значения 0–29,
		// то есть в среднем 30 % запросов.
		if randIntN(100) < cfg.MoviesMigrationPc {
			return targetMovies
		}
		return targetMonolith
	case hasPrefix(path, "/api/events"):
		return targetEvents
	default:
		// Пользователи, платежи, подписки и всё, что ещё не вынесено.
		return targetMonolith
	}
}

// hasPrefix — путь равен prefix или начинается с prefix + "/".
// Так /api/movies и /api/movies/health совпадают, а /api/moviesX — нет.
func hasPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// newReverseProxy создаёт прокси к одному сервису. Если сервис недоступен,
// клиент получает 502 с понятным JSON вместо обрыва соединения.
func newReverseProxy(target *url.URL, name string) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(target)
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("ошибка при обращении к %s (%s %s): %v", name, r.Method, r.URL.RequestURI(), err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "upstream unavailable",
			"service": name,
		})
	}
	return p
}

// loadConfig читает переменные окружения; при ошибке в адресах сервис
// не стартует (лучше упасть сразу, чем молча слать запросы не туда).
func loadConfig() config {
	pc := getEnvInt("MOVIES_MIGRATION_PERCENT", 0)
	if pc < 0 {
		pc = 0
	}
	if pc > 100 {
		pc = 100
	}
	return config{
		Port:              getEnv("PORT", "8000"),
		MonolithURL:       mustParseURL("MONOLITH_URL", "http://monolith:8080"),
		MoviesURL:         mustParseURL("MOVIES_SERVICE_URL", "http://movies-service:8081"),
		EventsURL:         mustParseURL("EVENTS_SERVICE_URL", "http://events-service:8082"),
		GradualMigration:  getEnvBool("GRADUAL_MIGRATION", true),
		MoviesMigrationPc: pc,
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	v, err := strconv.Atoi(getEnv(key, strconv.Itoa(def)))
	if err != nil {
		log.Fatalf("переменная %s должна быть целым числом: %v", key, err)
	}
	return v
}

func getEnvBool(key string, def bool) bool {
	v, err := strconv.ParseBool(getEnv(key, strconv.FormatBool(def)))
	if err != nil {
		log.Fatalf("переменная %s должна быть true или false: %v", key, err)
	}
	return v
}

func mustParseURL(key, def string) *url.URL {
	raw := getEnv(key, def)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Fatalf("переменная %s содержит некорректный адрес %q", key, raw)
	}
	return u
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
