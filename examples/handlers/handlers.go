package handlers

import (
	"fmt"
	"log"
	"time"

	"github.com/mrjvadi/go-broker/broker" // مسیر ایمپورت بر اساس ساختار پروژه شما
)

// --- پردازشگر برای کارهای حیاتی (Tasks) ---

// OrderPayload ساختار داده برای یک سفارش جدید است.
type OrderPayload struct {
	ID string `json:"id"`
}

// ProcessNewOrder یک کار از نوع "orders.PROCESS" را پردازش می‌کند.
func ProcessNewOrder(c *broker.Context) error {
	var p OrderPayload
	if err := c.Bind(&p); err != nil {
		return err
	}
	log.Printf("[TASK] Processing order %s...", p.ID)
	time.Sleep(2 * time.Second) // شبیه‌سازی یک کار سنگین
	log.Printf("[TASK] Order %s processed successfully.", p.ID)
	return nil
}

// --- پردازشگر برای رویدادهای آنی (Events) ---

// UserLoginPayload ساختار داده برای رویداد لاگین کاربر است.
type UserLoginPayload struct {
	Username string `json:"username"`
}

// LogUserLogin یک رویداد از کانال "users.login" را پردازش می‌کند.
func LogUserLogin(c *broker.Context) error {
	var p UserLoginPayload
	if err := c.Bind(&p); err != nil {
		return err
	}
	log.Printf("[EVENT] User '%s' logged in.", p.Username)
	return nil
}

// --- پردازشگر برای درخواست/پاسخ (RPC) ---

// UserInfoRequest ساختار داده برای درخواست اطلاعات کاربر است.
type UserInfoRequest struct {
	ID uint `json:"id"`
}

// UserInfoResponse ساختار داده برای پاسخ اطلاعات کاربر است.
type UserInfoResponse struct {
	ID    uint   `json:"id"`
	Email string `json:"email"`
}

// GetUserInfo یک درخواست RPC از نوع "users.GET_INFO" را پردازش کرده و پاسخ می‌دهد.
func GetUserInfo(c *broker.Context) (interface{}, error) {
	var req UserInfoRequest
	if err := c.Bind(&req); err != nil {
		return nil, err
	}
	log.Printf("[RPC] Received request to get info for user_id: %d", req.ID)
	time.Sleep(1 * time.Second) // شبیه‌سازی جستجو در دیتابیس

	// ساخت پاسخ و بازگرداندن آن. فریمورک به طور خودکار آن را به JSON تبدیل می‌کند.
	resp := UserInfoResponse{ID: req.ID, Email: fmt.Sprintf("user%d@example.com", req.ID)}
	return resp, nil
}
