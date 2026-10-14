package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

// Event — конверт события, как в api-specification.yaml (схема Event)
type Event struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   map[string]any `json:"payload"`
}

// Тип события → топик Kafka и обязательные поля (из api-specification.yaml)
var eventTypes = map[string]struct {
	topic    string
	required []string
}{
	"movie":   {"movie-events", []string{"movie_id", "title", "action"}},
	"user":    {"user-events", []string{"user_id", "action", "timestamp"}},
	"payment": {"payment-events", []string{"payment_id", "user_id", "amount", "status", "timestamp"}},
}

func main() {
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")

	// 1. Producer: один на все топики, топик указываем в каждом сообщении
	writer := &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		RequiredAcks:           kafka.RequireOne,      // ждём подтверждения от Kafka
		BatchTimeout:           10 * time.Millisecond, // по умолчанию копит пачку 1 с — слишком долго для API
		AllowAutoTopicCreation: true,
	}

	// 2. Consumer: по горутине на каждый топик, читаем и пишем в лог
	for _, t := range eventTypes {
		go consume(brokers, t.topic)
	}

	// 3. HTTP API
	http.HandleFunc("/api/events/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": true})
	})
	http.HandleFunc("/api/events/", func(w http.ResponseWriter, r *http.Request) {
		createEvent(writer, w, r)
	})

	log.Printf("Events service запущен на :%s, Kafka: %v", os.Getenv("PORT"), brokers)
	log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), nil))
}

// createEvent обрабатывает POST /api/events/{movie|user|payment}
func createEvent(writer *kafka.Writer, w http.ResponseWriter, r *http.Request) {
	eventType := strings.TrimPrefix(r.URL.Path, "/api/events/")
	t, ok := eventTypes[eventType]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "неизвестный тип события: " + eventType})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "нужен POST"})
		return
	}

	// Тело запроса → map; проверяем обязательные поля
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "некорректный JSON: " + err.Error()})
		return
	}
	for _, field := range t.required {
		if _, ok := payload[field]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "нет обязательного поля: " + field})
			return
		}
	}

	// Заворачиваем в конверт и отправляем в Kafka
	event := Event{ID: newID(), Type: eventType, Timestamp: time.Now().UTC(), Payload: payload}
	value, _ := json.Marshal(event)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	err := writer.WriteMessages(ctx, kafka.Message{Topic: t.topic, Key: []byte(event.ID), Value: value})
	if err != nil {
		log.Printf("ОШИБКА отправки в %s: %v", t.topic, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Kafka недоступна: " + err.Error()})
		return
	}

	log.Printf("ОТПРАВЛЕНО в %s: id=%s", t.topic, event.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"status": "success", "event": event})
}

// consume бесконечно читает топик и пишет каждое событие в лог.
// Если Kafka ещё не поднялась или пропала — ждём и пробуем снова.
func consume(brokers []string, topic string) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: "events-service", // Kafka запоминает, что мы уже прочитали
	})
	log.Printf("Consumer подписан на %s", topic)

	for {
		msg, err := reader.ReadMessage(context.Background())
		if err != nil {
			log.Printf("ОШИБКА чтения %s: %v — повтор через 5 с", topic, err)
			time.Sleep(5 * time.Second)
			continue
		}
		log.Printf("ПРОЧИТАНО из %s [partition=%d offset=%d]: %s",
			topic, msg.Partition, msg.Offset, msg.Value)
	}
}

// newID — случайный идентификатор события (16 hex-символов)
func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// writeJSON отвечает клиенту JSON-ом с нужным HTTP-кодом
func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(body)
}
