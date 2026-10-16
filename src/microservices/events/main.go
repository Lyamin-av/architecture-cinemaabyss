package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
)

// Тип события → топик Kafka
var topics = map[string]string{
	"movie":   "movie-events",
	"user":    "user-events",
	"payment": "payment-events",
}

func main() {
	broker := os.Getenv("KAFKA_BROKERS")

	// Producer: отправляет события в Kafka
	writer := &kafka.Writer{
		Addr:         kafka.TCP(broker),
		RequiredAcks: kafka.RequireOne,      // ждём подтверждения записи
		BatchTimeout: 10 * time.Millisecond, // не копить сообщения 1 с
	}

	// Consumer: по одному на топик, читает события и пишет в лог
	for _, topic := range topics {
		go consume(broker, topic)
	}

	http.HandleFunc("GET /api/events/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": true})
	})

	// {type} — часть пути: movie, user или payment
	http.HandleFunc("POST /api/events/{type}", func(w http.ResponseWriter, r *http.Request) {
		eventType := r.PathValue("type")
		topic, ok := topics[eventType]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "неизвестный тип события"})
			return
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "некорректный JSON"})
			return
		}

		event := map[string]any{
			"id":        fmt.Sprintf("%s-%d", eventType, time.Now().UnixNano()),
			"type":      eventType,
			"timestamp": time.Now().UTC(),
			"payload":   payload,
		}
		value, _ := json.Marshal(event)

		if err := writer.WriteMessages(r.Context(), kafka.Message{Topic: topic, Value: value}); err != nil {
			log.Printf("ОШИБКА отправки в %s: %v", topic, err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}

		log.Printf("ОТПРАВЛЕНО в %s: %s", topic, value)
		writeJSON(w, http.StatusCreated, map[string]any{"status": "success", "event": event})
	})

	log.Printf("Events service запущен на :%s", os.Getenv("PORT"))
	log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), nil))
}

// consume бесконечно читает топик; при ошибке ждёт 5 с и пробует снова
func consume(broker, topic string) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{broker},
		Topic:   topic,
		GroupID: "events-service",
	})
	for {
		msg, err := reader.ReadMessage(context.Background())
		if err != nil {
			log.Printf("ОШИБКА чтения %s: %v", topic, err)
			time.Sleep(5 * time.Second)
			continue
		}
		log.Printf("ПРОЧИТАНО из %s: %s", topic, msg.Value)
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(body)
}
