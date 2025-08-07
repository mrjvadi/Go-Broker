package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// Task handler
type OrderPayload struct {
	ID string `json:"id"`
}

func ProcessNewOrder(ctx context.Context, payload []byte) error {
	var p OrderPayload
	json.Unmarshal(payload, &p)
	log.Printf("[TASK] Processing order %s...", p.ID)
	time.Sleep(2 * time.Second)
	log.Printf("[TASK] Order %s processed.", p.ID)
	return nil
}

// Event handler
type UserLoginPayload struct {
	Username string `json:"username"`
}

func LogUserLogin(ctx context.Context, payload []byte) error {
	var p UserLoginPayload
	json.Unmarshal(payload, &p)
	log.Printf("[EVENT] User '%s' logged in.", p.Username)
	return nil
}

// RPC handler
type UserInfoRequest struct {
	ID uint `json:"id"`
}
type UserInfoResponse struct {
	ID    uint   `json:"id"`
	Email string `json:"email"`
}

func GetUserInfo(ctx context.Context, payload []byte) ([]byte, error) {
	var req UserInfoRequest
	json.Unmarshal(payload, &req)
	log.Printf("[RPC] Request for user info: %d", req.ID)
	time.Sleep(1 * time.Second)
	resp := UserInfoResponse{ID: req.ID, Email: fmt.Sprintf("user%d@example.com", req.ID)}
	return json.Marshal(resp)
}
