package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/auth/credentials/idtoken"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
)

var userConnections = make(map[int]*websocket.Conn)
var mutex = &sync.Mutex{}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Helper Functions
func sendJSON(conn *websocket.Conn, data map[string]interface{}) {
	conn.WriteJSON(data)
}

func sendErrorToClient(conn *websocket.Conn, errMsg string) {
	sendJSON(conn, map[string]interface{}{"action": "error", "message": errMsg})
}

// ======================================================================LOGIN========================================================================//
func handleEmailRegister(conn *websocket.Conn, req ActionRequest) {
	if req.Email == "" || req.Password == "" || req.Username == "" {
		sendErrorToClient(conn, "Missing required fields")
		return
	}

	// รัน bcrypt ใน goroutine แยก เพื่อไม่ block WebSocket loop
	// bcrypt.MinCost (4) เร็วกว่า DefaultCost (10) ~64x และยังปลอดภัยเพียงพอสำหรับ dev/prod ทั่วไป
	// ถ้าต้องการความปลอดภัยสูงสุดเปลี่ยนเป็น bcrypt.DefaultCost ได้ แต่จะช้าลง ~300ms
	go func() {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			sendErrorToClient(conn, "Error securing password")
			return
		}

		// INSERT เลย แล้วให้ UNIQUE constraint จัดการ — ลด round-trip DB จาก 3 เหลือ 1
		var newUserID int
		err = db.QueryRow(
			"INSERT INTO users (email, username, password_hash) VALUES ($1, $2, $3) RETURNING id",
			req.Email, req.Username, string(hashedPassword),
		).Scan(&newUserID)

		if err != nil {
			errMsg := err.Error()
			if strings.Contains(errMsg, "users_username_key") || (strings.Contains(errMsg, "unique") && strings.Contains(errMsg, "username")) {
				sendErrorToClient(conn, "Username already exists")
			} else if strings.Contains(errMsg, "users_email_key") || (strings.Contains(errMsg, "unique") && strings.Contains(errMsg, "email")) {
				sendErrorToClient(conn, "Email already exists")
			} else {
				sendErrorToClient(conn, "Registration failed")
			}
			return
		}

		fmt.Printf("✅ User %s Registered successfully! (id=%d)\n", req.Username, newUserID)
		sendJSON(conn, map[string]interface{}{
			"action":  "register_success",
			"message": "สมัครสมาชิกสำเร็จแล้ว!",
		})
	}()
}

func handleLogin(conn *websocket.Conn, req ActionRequest, loggedInUserID *int) {
	if req.Username == "" || req.Password == "" {
		sendErrorToClient(conn, "กรุณากรอกข้อมูลให้ครบ")
		return
	}

	var userID int
	var passwordHash, email, dbUsername string

	query := `SELECT id, email, password_hash, username FROM users WHERE email = $1 OR username = $1`
	err := db.QueryRow(query, req.Username).Scan(&userID, &email, &passwordHash, &dbUsername)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
		sendErrorToClient(conn, "ชื่อผู้ใช้ อีเมล หรือ รหัสผ่านไม่ถูกต้อง")
		return
	}

	appToken, _ := generateJWT(userID, email)

	mutex.Lock()
	userConnections[userID] = conn
	*loggedInUserID = userID
	mutex.Unlock()

	sendJSON(conn, map[string]interface{}{
		"jwt":      appToken,
		"user_id":  userID,
		"username": dbUsername,
	})
	fmt.Printf("✅ User %s Logged in successfully!\n", req.Username)
}

func handleGoogleLogin(conn *websocket.Conn, req ActionRequest, loggedInUserID *int) {
	if req.Token == "" {
		sendErrorToClient(conn, "Token is empty")
		return
	}

	payload, err := idtoken.Validate(context.Background(), req.Token, googleClientID)
	if err != nil {
		sendErrorToClient(conn, "Invalid Google Token")
		return
	}

	email := payload.Claims["email"].(string)
	name := payload.Claims["name"].(string)

	userID, err := getOrCreateUserByEmail(email, name)
	if err != nil {
		sendErrorToClient(conn, "Error verifying user")
		return
	}

	appToken, err := generateJWT(userID, email)
	if err != nil {
		sendErrorToClient(conn, "Error generating token")
		return
	}

	mutex.Lock()
	userConnections[userID] = conn
	*loggedInUserID = userID
	mutex.Unlock()

	sendJSON(conn, map[string]interface{}{
		"jwt":      appToken,
		"user_id":  userID,
		"username": name,
	})
}

// ======================================================================POST========================================================================//
func sendHistoryToClient(conn *websocket.Conn, userID int) {
	posts, err := getFeedPostsWithUser(userID)
	if err != nil {
		fmt.Println("Error loading posts:", err)
		return
	}

	sendJSON(conn, map[string]interface{}{
		"action": "post_history",
		"data":   posts,
	})
}

func handleCreatePost(req ActionRequest) {
	if req.UserID == 0 {
		return
	}

	newPostID, err := createPost(req.UserID, req.Content, req.ImageURLs, nil)
	if err == nil {
		newPostData, _ := getSinglePost(newPostID)
		responseMap := map[string]interface{}{"action": "new_post", "data": newPostData}
		broadcast(responseMap)
	}
}

func handleDeletePost(conn *websocket.Conn, req ActionRequest) {
	if req.UserID == 0 || req.PostID == 0 {
		sendErrorToClient(conn, "Missing UserID or PostID")
		return
	}

	err := deletePost(req.PostID, req.UserID)
	if err != nil {
		sendErrorToClient(conn, "ไม่สามารถลบโพสต์ได้ หรือคุณไม่ใช่เจ้าของโพสต์")
		return
	}

	broadcast(map[string]interface{}{
		"action":  "post_deleted",
		"post_id": req.PostID,
	})
}

func handleLike(conn *websocket.Conn, req ActionRequest, loggedInUserID int) {
	if loggedInUserID == 0 {
		sendErrorToClient(conn, "Please login first")
		return
	}

	status, err := toggleLike(loggedInUserID, req.PostID)
	if err != nil {
		sendErrorToClient(conn, "Failed to toggle like")
		return
	}

	fmt.Printf("User %d toggled like for post %d. New status: %v\n", loggedInUserID, req.PostID, status)
}

func handleRepost(conn *websocket.Conn, req ActionRequest, loggedInUserID int) {
	if loggedInUserID == 0 {
		sendErrorToClient(conn, "Please login first")
		return
	}

	status, err := toggleRepost(loggedInUserID, req.PostID)
	if err != nil {
		sendErrorToClient(conn, "Failed to toggle repost")
		return
	}

	fmt.Printf("User %d toggled repost for post %d. New status: %v\n", loggedInUserID, req.PostID, status)
}

func handleBookmark(conn *websocket.Conn, req ActionRequest, loggedInUserID int) {
	if loggedInUserID == 0 {
		sendErrorToClient(conn, "Please login first")
		return
	}

	status, err := toggleBookmark(loggedInUserID, req.PostID)
	if err != nil {
		sendErrorToClient(conn, "Failed to toggle bookmark")
		return
	}

	fmt.Printf("User %d toggled bookmark for post %d. New status: %v\n", loggedInUserID, req.PostID, status)
}

// ======================================================================FOLLOW========================================================================//

// handleToggleFollow — รับ action "toggle_follow" แล้วบันทึก/ยกเลิก follow ลงตาราง follows
func handleToggleFollow(conn *websocket.Conn, req ActionRequest) {
	if req.UserID == 0 || req.TargetUserID == 0 {
		sendErrorToClient(conn, "Missing user_id or target_user_id")
		return
	}

	// ป้องกัน follow ตัวเอง
	if req.UserID == req.TargetUserID {
		sendErrorToClient(conn, "Cannot follow yourself")
		return
	}

	isFollowing, err := toggleFollow(req.UserID, req.TargetUserID)
	if err != nil {
		sendErrorToClient(conn, "Failed to toggle follow")
		return
	}

	fmt.Printf("User %d toggled follow for user %d. Now following: %v\n", req.UserID, req.TargetUserID, isFollowing)

	// ส่งผลลัพธ์กลับไปที่คนกด follow
	sendJSON(conn, map[string]interface{}{
		"action":       "follow_result",
		"is_following": isFollowing,
		"target_user":  req.TargetUserID,
	})

	// 🟢 ถ้าเป็นการ Follow ใหม่ (ไม่ใช่ Unfollow) ให้ส่งแจ้งเตือนไปที่เป้าหมายด้วย
	if isFollowing {
		// ดึงชื่อ User ที่กด Follow
		var followerName string
		db.QueryRow("SELECT username FROM users WHERE id=$1", req.UserID).Scan(&followerName)

		// ตรวจสอบว่าเป้าหมาย Follow เรากลับอยู่หรือเปล่า
		theyFollowBack, _ := getFollowStatus(req.TargetUserID, req.UserID)

		sendMessageToUser(req.TargetUserID, map[string]interface{}{
			"action": "new_notification",
			"data": map[string]interface{}{
				"type":              "follow",
				"user_id":           req.UserID,
				"username":          followerName,
				"target_user_id":    req.TargetUserID,
				"content":           "",
				"is_followed_by_me": theyFollowBack, // เป้าหมาย follow เราอยู่ไหม
				"is_following_me":   false,          // เราเพิ่งกด follow เขา ไม่ใช่เขา follow เรา
			},
		})
	}
}

// ======================================================================USER========================================================================//
func handleUpdateProfile(conn *websocket.Conn, req ActionRequest) {
	if req.UserID == 0 {
		sendErrorToClient(conn, "Unauthorized")
		return
	}

	err := updateUserProfile(req.UserID, req.Username, req.ImageURL, req.ImageURLs[0])
	if err != nil {
		sendErrorToClient(conn, "Failed to update profile")
		return
	}

	sendJSON(conn, map[string]interface{}{
		"action":  "profile_updated",
		"message": "อัปเดตโปรไฟล์สำเร็จ",
	})
}

func handleChangePassword(conn *websocket.Conn, req ActionRequest) {
	fmt.Printf("handleChangePassword called: userID=%d, oldPassword=%s, newPassword=%s\n", req.UserID, req.OldPassword, req.Password)

	if req.UserID == 0 {
		sendErrorToClient(conn, "Unauthorized")
		return
	}

	if req.OldPassword == "" || req.Password == "" {
		sendErrorToClient(conn, "กรุณากรอกรหัสผ่านเดิมและรหัสผ่านใหม่")
		return
	}

	err := changePassword(req.UserID, req.OldPassword, req.Password)
	if err != nil {
		fmt.Printf("changePassword error: %v\n", err)
		if err.Error() == "incorrect current password" {
			sendErrorToClient(conn, "รหัสผ่านเดิมไม่ถูกต้อง")
		} else {
			sendErrorToClient(conn, "เปลี่ยนรหัสผ่านไม่สำเร็จ: "+err.Error())
		}
		return
	}

	fmt.Println("Password changed successfully")
	sendJSON(conn, map[string]interface{}{
		"action":  "password_changed",
		"message": "เปลี่ยนรหัสผ่านสำเร็จ",
	})
}

// ======================================================================MESSAGE========================================================================//
func sendMessageToUser(userID int, data map[string]interface{}) {
	mutex.Lock()
	defer mutex.Unlock()
	if conn, ok := userConnections[userID]; ok {
		conn.WriteJSON(data)
	}
}

func handleGetChatList(conn *websocket.Conn, req ActionRequest) {
	if req.UserID == 0 {
		sendErrorToClient(conn, "Missing UserID")
		return
	}

	chatList, err := getChatList(req.UserID)
	if err != nil {
		sendErrorToClient(conn, "Failed to load chat list")
		return
	}

	sendJSON(conn, map[string]interface{}{
		"action": "load_chat_list",
		"data":   chatList,
	})
}

func handleGetNotifications(conn *websocket.Conn, req ActionRequest) {
	if req.UserID == 0 {
		sendErrorToClient(conn, "Missing UserID")
		return
	}

	var notifications []map[string]interface{}

	// 1. ดึง follow notifications จากตาราง follows
	followRows, err := db.Query(`
		SELECT f.follower_id, u.username,
		       EXISTS(SELECT 1 FROM follows WHERE follower_id=$1 AND following_id=f.follower_id) AS i_follow_them,
		       f.created_at
		FROM follows f
		JOIN users u ON u.id = f.follower_id
		WHERE f.following_id = $1
		ORDER BY f.created_at DESC
		LIMIT 50`, req.UserID)

	if err == nil {
		defer followRows.Close()
		for followRows.Next() {
			var followerID int
			var followerName string
			var iFollowThem bool
			var createdAt time.Time
			if err := followRows.Scan(&followerID, &followerName, &iFollowThem, &createdAt); err == nil {
				notifications = append(notifications, map[string]interface{}{
					"type":          "follow",
					"user":          followerName,
					"sender_id":     followerID,
					"content":       "",
					"i_follow_them": iFollowThem,
					"created_at":    createdAt,
				})
			}
		}
	}

	// 2. ดึง DM notifications (ข้อความล่าสุดจากแต่ละคน)
	msgRows, err := db.Query(`
		WITH ranked AS (
			SELECT
				m.sender_id,
				u.username AS sender_name,
				COALESCE(m.content, '') AS content,
				m.created_at,
				ROW_NUMBER() OVER (PARTITION BY m.sender_id ORDER BY m.created_at DESC) AS rn
			FROM messages m
			JOIN users u ON u.id = m.sender_id
			WHERE m.receiver_id = $1 AND m.sender_id != $1
		)
		SELECT sender_id, sender_name, content, created_at
		FROM ranked WHERE rn = 1
		ORDER BY created_at DESC
		LIMIT 50`, req.UserID)

	if err == nil {
		defer msgRows.Close()
		for msgRows.Next() {
			var senderID int
			var senderName, content string
			var createdAt time.Time
			if err := msgRows.Scan(&senderID, &senderName, &content, &createdAt); err == nil {
				notifications = append(notifications, map[string]interface{}{
					"type":        "message",
					"user":        senderName,
					"sender_id":   senderID,
					"sender_name": senderName,
					"content":     content,
					"created_at":  createdAt,
				})
			}
		}
	}

	// เรียงตาม created_at ล่าสุดก่อน
	for i := 0; i < len(notifications)-1; i++ {
		for j := i + 1; j < len(notifications); j++ {
			ti := notifications[i]["created_at"].(time.Time)
			tj := notifications[j]["created_at"].(time.Time)
			if tj.After(ti) {
				notifications[i], notifications[j] = notifications[j], notifications[i]
			}
		}
	}

	if notifications == nil {
		notifications = []map[string]interface{}{}
	}

	sendJSON(conn, map[string]interface{}{
		"action": "load_notifications",
		"data":   notifications,
	})
}

func handleGetChatHistory(conn *websocket.Conn, req ActionRequest) {
	if req.UserID == 0 || req.ReceiverID == 0 {
		sendErrorToClient(conn, "Missing UserID or ReceiverID")
		return
	}

	history, err := getChatHistory(req.UserID, req.ReceiverID)
	if err != nil {
		sendErrorToClient(conn, "Failed to load chat history")
		return
	}

	sendJSON(conn, map[string]interface{}{
		"action": "load_chat_history",
		"data":   history,
	})
}

func handleSendMessage(req ActionRequest) {
	if req.UserID == 0 || req.ReceiverID == 0 {
		return
	}

	msgID, err := saveMessage(req.UserID, req.ReceiverID, req.Content, req.ImageURL)
	if err == nil {
		fullMsg, _ := getMessageByID(msgID)

		// ดึงชื่อผู้ส่งเพื่อใส่ใน payload แจ้งเตือน
		var senderName string
		db.QueryRow("SELECT username FROM users WHERE id=$1", req.UserID).Scan(&senderName)

		responseMap := map[string]interface{}{
			"action": "new_message",
			"data": map[string]interface{}{
				"id":          fullMsg.ID,
				"sender_id":   fullMsg.SenderID,
				"receiver_id": fullMsg.ReceiverID,
				"content":     fullMsg.Content,
				"image_url":   fullMsg.ImageURL,
				"is_read":     fullMsg.IsRead,
				"created_at":  fullMsg.CreatedAt,
				"sender_name": senderName, // 🟢 เพิ่มชื่อผู้ส่งสำหรับแจ้งเตือน
			},
		}

		sendMessageToUser(req.ReceiverID, responseMap)
		sendMessageToUser(req.UserID, responseMap)
	}
}

func handleDeleteMessage(conn *websocket.Conn, req ActionRequest) {
	msgID := req.PostID

	if req.UserID == 0 || msgID == 0 {
		sendErrorToClient(conn, "Invalid request")
		return
	}

	err := deleteMessage(msgID, req.UserID)
	if err != nil {
		sendErrorToClient(conn, "ไม่สามารถยกเลิกข้อความได้")
		return
	}

	if req.ReceiverID != 0 {
		sendMessageToUser(req.ReceiverID, map[string]interface{}{
			"action":     "message_deleted",
			"message_id": msgID,
		})
	}
}

func broadcast(data map[string]interface{}) {
	mutex.Lock()
	defer mutex.Unlock()
	for _, conn := range userConnections {
		conn.WriteJSON(data)
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("WebSocket Upgrade Error:", err)
		return
	}

	var loggedInUserID int
	defer func() {
		conn.Close()
		if loggedInUserID != 0 {
			mutex.Lock()
			delete(userConnections, loggedInUserID)
			mutex.Unlock()
			fmt.Printf("User %d disconnected\n", loggedInUserID)
		}
	}()

	for {
		var req ActionRequest
		err := conn.ReadJSON(&req)
		if err != nil {
			break
		}

		switch req.Action {
		case "email_register":
			handleEmailRegister(conn, req)
		case "login":
			handleLogin(conn, req, &loggedInUserID)
		case "google_login":
			handleGoogleLogin(conn, req, &loggedInUserID)
		case "register_connection":
			loggedInUserID = req.UserID
			sendHistoryToClient(conn, loggedInUserID)

			mutex.Lock()
			userConnections[loggedInUserID] = conn
			mutex.Unlock()

			fmt.Println("User registered:", loggedInUserID)

		case "fetch_account_info": // 🟢 สำหรับดึงข้อมูลมาโชว์ตอนเปิดหน้า
			accountInfo, err := getAccountInfoFromDB(req.UserID) // หรือใช้ฟังก์ชัน getUserByID ของคุณ
			if err != nil {
				sendErrorToClient(conn, "ไม่สามารถดึงข้อมูลได้")
			} else {
				sendJSON(conn, map[string]interface{}{
					"action": "account_info_response",
					"data":   accountInfo,
				})
			}

		case "update_account_field": // 🟢 สำหรับอัปเดตข้อมูลจาก Dialog
			// ดึงฟังก์ชัน updateAccountField จาก database.go มาใช้งาน
			err := updateAccountField(req.UserID, req.Field, req.Content)
			if err != nil {
				sendErrorToClient(conn, "อัปเดตข้อมูลไม่สำเร็จ: "+err.Error())
			} else {
				sendJSON(conn, map[string]interface{}{
					"action": "update_success",
				})
			}

		case "change_password":
			handleChangePassword(conn, req)

		case "send_message":
			handleSendMessage(req)
		case "get_chat_list":
			handleGetChatList(conn, req)
		case "get_chat_history":
			handleGetChatHistory(conn, req)
		case "get_notifications":
			handleGetNotifications(conn, req)
		case "delete_message":
			handleDeleteMessage(conn, req)

		// 🟢 [ใหม่] Follow / Unfollow
		case "toggle_follow":
			handleToggleFollow(conn, req)

		case "fetch_profile_data":
			posts, _ := GetUserPosts(req.TargetUserID, req.UserID)
			reposts, _ := GetUserReposts(req.TargetUserID, req.UserID)
			favorites, _ := GetUserFavorites(req.TargetUserID, req.UserID)

			// 🟢 ส่งสถานะ is_following ไปด้วย
			isFollowing, _ := getFollowStatus(req.UserID, req.TargetUserID)

			sendJSON(conn, map[string]interface{}{
				"action":       "profile_data_response",
				"posts":        posts,
				"reposts":      reposts,
				"favorites":    favorites,
				"is_following": isFollowing, // 🟢
			})

		case "create_post":
			handleCreatePost(req)
		case "toggle_like":
			handleLike(conn, req, loggedInUserID)
		case "toggle_repost":
			handleRepost(conn, req, loggedInUserID)
		case "toggle_bookmark":
			handleBookmark(conn, req, loggedInUserID)
		case "update_profile":
			handleUpdateProfile(conn, req)
		case "delete_post":
			handleDeletePost(conn, req)

		case "fetch_bookmarks":
			if req.UserID == 0 {
				sendErrorToClient(conn, "Unauthorized")
				return
			}
			posts, err := GetUserBookmarks(req.UserID)
			if err != nil {
				sendErrorToClient(conn, "Failed to load bookmarks")
				return
			}
			sendJSON(conn, map[string]interface{}{
				"action": "bookmarks_response",
				"data":   posts,
			})

		default:
			sendErrorToClient(conn, "Unknown action")
		}
	}
}
