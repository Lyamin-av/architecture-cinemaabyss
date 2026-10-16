package main

import (
	"log"
	"math/rand/v2"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
)

func main() {
	// 1. Читаем настройки из переменных окружения
	monolithURL, _ := url.Parse(os.Getenv("MONOLITH_URL"))
	moviesURL, _ := url.Parse(os.Getenv("MOVIES_SERVICE_URL"))
	eventsURL, _ := url.Parse(os.Getenv("EVENTS_SERVICE_URL"))
	gradual := os.Getenv("GRADUAL_MIGRATION") == "true"
	percent, _ := strconv.Atoi(os.Getenv("MOVIES_MIGRATION_PERCENT"))

	// Процент ограничиваем диапазоном 0–100
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	// 2. Готовые прокси к каждому сервису
	monolith := httputil.NewSingleHostReverseProxy(monolithURL)
	movies := httputil.NewSingleHostReverseProxy(moviesURL)
	events := httputil.NewSingleHostReverseProxy(eventsURL)

	// forward пересылает запрос в выбранный сервис и пишет об этом в лог
	forward := func(name string, p *httputil.ReverseProxy, w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s -> %s", r.Method, r.URL.Path, name)
		w.Header().Set("X-Routed-To", name)
		p.ServeHTTP(w, r)
	}

	// 3. Маршруты
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":true}`))
	})

	moviesHandler := func(w http.ResponseWriter, r *http.Request) {
		if !gradual || rand.IntN(100) < percent {
			forward("movies-service", movies, w, r)
		} else {
			forward("monolith", monolith, w, r)
		}
	}
	http.HandleFunc("/api/movies", moviesHandler)  // точный путь /api/movies
	http.HandleFunc("/api/movies/", moviesHandler) // и всё, что ниже: /api/movies/health

	http.HandleFunc("/api/events/", func(w http.ResponseWriter, r *http.Request) {
		forward("events-service", events, w, r)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		forward("monolith", monolith, w, r)
	})

	// 4. Запуск
	log.Printf("Proxy запущен на :%s; GRADUAL_MIGRATION=%t, MOVIES_MIGRATION_PERCENT=%d",
		os.Getenv("PORT"), gradual, percent)
	log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), nil))
}
