package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	fmt.Println("เช็คค่า DB_URL:", os.Getenv("DB_URL"))

	initDB()
	defer db.Close()

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	// เพิ่ม Route สำหรับ Search (HTTP GET)
	http.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		keyword := r.URL.Query().Get("q")
		// รับ userId จาก Query String (เช่น /api/search?q=abc&userId=123)
		userIDStr := r.URL.Query().Get("userID")
		var userID int
		fmt.Sscanf(userIDStr, "%d", &userID)

		if keyword == "" {
			http.Error(w, `{"error": "Missing query parameter 'q'"}`, http.StatusBadRequest)
			return
		}

		// ส่ง userID เข้าไปในฟังก์ชันค้นหาด้วย
		users, posts, err := searchUsersAndPosts(keyword, userID)
		if err != nil {
			http.Error(w, `{"error": "Internal server error"}`, http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"users": users,
			"posts": posts,
		})
	})

	// WebSocket Route เดิมของคุณ
	http.HandleFunc("/", handleConnections)
	http.HandleFunc("/ws", handleConnections)

	fmt.Println("🚀 Server Started on port", port, "...")
	log.Fatal(http.ListenAndServe("0.0.0.0:"+port, nil))
}
