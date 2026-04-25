package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

var db *sql.DB

func initDB() {
	connStr := os.Getenv("DB_URL")
	var err error
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Error opening database:", err)
	}

	if err = db.Ping(); err != nil {
		log.Fatal("Cannot connect to Database:", err)
	}
	fmt.Println("✅ Connected to Database successfully!")
}

func fetchPostsWithStatus(query string, args ...interface{}) ([]map[string]interface{}, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []map[string]interface{}
	for rows.Next() {
		var id, userId int
		var username string
		var profileImage, content sql.NullString
		var imageUrls pq.StringArray
		var parentPostId *int
		var createdAt time.Time
		var likesCount, repostsCount int
		var isLiked, isReposted, isBookmarked bool

		err := rows.Scan(
			&id, &userId, &username, &profileImage, &content,
			&imageUrls, &parentPostId, &createdAt,
			&likesCount, &isLiked,
			&repostsCount, &isReposted,
			&isBookmarked,
		)
		if err != nil {
			fmt.Println("❌ Scan Error:", err)
			continue
		}

		posts = append(posts, map[string]interface{}{
			"post_id":           id,
			"user_id":           userId,
			"username":          username,
			"profile_image_url": profileImage.String,
			"content":           content.String,
			"image_urls":        []string(imageUrls),
			"parent_post_id":    parentPostId,
			"created_at":        createdAt,
			"likes_count":       likesCount,
			"is_liked":          isLiked,
			"reposts_count":     repostsCount,
			"is_reposted":       isReposted,
			"is_bookmarked":     isBookmarked,
		})
	}
	return posts, nil
}

// ======================================================================USER========================================================================//

func getAccountInfoFromDB(userID int) (map[string]interface{}, error) {
	var email, username string
	var phone, country, birthDate sql.NullString

	// ดึงข้อมูลตามโครงสร้าง Database ของคุณ
	err := db.QueryRow(`
		SELECT email, username, phone, country, birth_date 
		FROM users WHERE id = $1`, userID).
		Scan(&email, &username, &phone, &country, &birthDate)

	if err != nil {
		return nil, err
	}

	// จัดการเรื่องวันที่ (ตัดเอาแค่ YYYY-MM-DD)
	birthDateStr := ""
	if birthDate.Valid {
		birthDateStr = birthDate.String
		if len(birthDateStr) >= 10 {
			birthDateStr = birthDateStr[:10]
		}
	}

	return map[string]interface{}{
		"email":      email,
		"username":   username,
		"phone":      phone.String,   // ถ้าเป็น Null จะได้ ""
		"country":    country.String, // ถ้าเป็น Null จะได้ ""
		"birth_date": birthDateStr,
	}, nil
}

func updateAccountField(userID int, field string, value string) error {
	// Whitelist ป้องกัน SQL Injection เนื่องจากเราจะเอาตัวแปร field ไปต่อ String ตรงๆ
	allowedFields := map[string]bool{
		"username":   true,
		"email":      true,
		"phone":      true,
		"country":    true,
		"birth_date": true,
	}

	if !allowedFields[field] {
		return fmt.Errorf("invalid field name: %s", field)
	}

	// ใช้ fmt.Sprintf เพื่อกำหนดชื่อคอลัมน์แบบ Dynamic (ปลอดภัยเพราะผ่าน Whitelist แล้ว)
	query := fmt.Sprintf("UPDATE users SET %s = $1 WHERE id = $2", field)
	res, err := db.Exec(query, value, userID)
	if err != nil {
		return err
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func changePassword(userID int, oldPassword string, newPassword string) error {
	// 1. ดึง password_hash ปัจจุบัน
	var currentHash string
	err := db.QueryRow("SELECT password_hash FROM users WHERE id = $1", userID).Scan(&currentHash)
	if err != nil {
		return fmt.Errorf("user not found")
	}

	// 2. ตรวจสอบรหัสผ่านเดิม
	err = bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(oldPassword))
	if err != nil {
		return fmt.Errorf("incorrect current password")
	}

	// 3. Hash รหัสผ่านใหม่
	hashedNewPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("error hashing new password")
	}

	// 4. อัปเดตรหัสผ่านใหม่
	res, err := db.Exec("UPDATE users SET password_hash = $1 WHERE id = $2", string(hashedNewPassword), userID)
	if err != nil {
		return err
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("user not found")
	}

	return nil
}

func getOrCreateUserByEmail(email string, username string) (int, error) {
	var userID int
	err := db.QueryRow("SELECT id FROM users WHERE email = $1", email).Scan(&userID)
	if err == sql.ErrNoRows {
		err = db.QueryRow("INSERT INTO users (email, username, password_hash) VALUES ($1, $2, $3) RETURNING id", email, username, "GOOGLE_OAUTH").Scan(&userID)
		return userID, err
	}
	return userID, err
}

func getUserByID(userID int) (*User, error) {
	var user User
	// ใช้ sql.NullString เพื่อรองรับค่า NULL จาก Database
	var profileImage, coverImage, phone, country, birthDate sql.NullString

	err := db.QueryRow(`
		SELECT id, email, username, profile_image_url, cover_image_url, phone, country, birth_date, created_at 
		FROM users WHERE id = $1`, userID).
		Scan(
			&user.ID,
			&user.Email,
			&user.Username,
			&profileImage,
			&coverImage,
			&phone,     // 🟢
			&country,   // 🟢
			&birthDate, // 🟢
			&user.CreatedAt,
		)

	if err != nil {
		return nil, err
	}

	user.ProfileImageURL = profileImage.String
	user.CoverImageURL = coverImage.String

	// 🟢 Map ค่าลงใน Pointer ให้ถูกต้อง
	if phone.Valid {
		user.Phone = &phone.String
	}
	if country.Valid {
		user.Country = &country.String
	}
	if birthDate.Valid {
		// ตัดเวลาออก เอาเฉพาะ YYYY-MM-DD
		dateStr := birthDate.String[:10]
		user.BirthDate = &dateStr
	}

	return &user, nil
}

func updateUserProfile(userID int, username string, profileImageURL string, coverImageURL string) error {
	res, err := db.Exec(`
		UPDATE users 
		SET username = $1, profile_image_url = $2, cover_image_url = $3 
		WHERE id = $4`, username, profileImageURL, coverImageURL, userID)
	if err != nil {
		return err
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func deleteUser(userID int) error {
	_, err := db.Exec("DELETE FROM users WHERE id = $1", userID)
	return err
}

// ======================================================================FOLLOW========================================================================//

// getFollowStatus ตรวจสอบว่า followerID ติดตาม followingID อยู่หรือเปล่า
func getFollowStatus(followerID int, followingID int) (bool, error) {
	var exists bool
	err := db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM follows WHERE follower_id=$1 AND following_id=$2)",
		followerID, followingID,
	).Scan(&exists)
	return exists, err
}

// toggleFollow บันทึก/ยกเลิก follow ลงตาราง follows
// คืนค่า isFollowing = true ถ้าตอนนี้ follow อยู่ (เพิ่งกด follow)
func toggleFollow(followerID int, followingID int) (bool, error) {
	isFollowing, err := getFollowStatus(followerID, followingID)
	if err != nil {
		return false, err
	}

	if isFollowing {
		// Unfollow
		_, err = db.Exec(
			"DELETE FROM follows WHERE follower_id=$1 AND following_id=$2",
			followerID, followingID,
		)
		if err != nil {
			return true, err
		}
		return false, nil
	}

	// Follow
	_, err = db.Exec(
		"INSERT INTO follows (follower_id, following_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
		followerID, followingID,
	)
	if err != nil {
		return false, err
	}
	return true, nil
}

// ======================================================================POST========================================================================//
func createPost(userID int, content string, imageURLs []string, parentPostID *int) (int, error) {
	if imageURLs == nil {
		imageURLs = []string{}
	}
	var newPostID int
	err := db.QueryRow(`INSERT INTO posts (user_id, content, image_urls, parent_post_id) VALUES ($1, $2, $3, $4) RETURNING id`, userID, content, pq.Array(imageURLs), parentPostID).Scan(&newPostID)
	return newPostID, err
}

func getSinglePost(postID int) (*PostFeed, error) {
	var post PostFeed
	var imgURLs pq.StringArray
	err := db.QueryRow(`SELECT p.id, p.user_id, u.username, COALESCE(u.profile_image_url, ''), p.content, COALESCE(p.image_urls, '{}'), p.parent_post_id, (SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count, p.created_at FROM posts p JOIN users u ON p.user_id = u.id WHERE p.id = $1`, postID).Scan(&post.PostID, &post.UserID, &post.Username, &post.ProfileImageURL, &post.Content, &imgURLs, &post.ParentPostID, &post.LikeCount, &post.CreatedAt)
	post.ImageURLs = []string(imgURLs)
	return &post, err
}

func getFeedPosts() ([]PostFeed, error) {
	rows, err := db.Query(`SELECT p.id, p.user_id, u.username, COALESCE(u.profile_image_url, ''), p.content, COALESCE(p.image_urls, '{}'), p.parent_post_id, (SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count, p.created_at FROM posts p JOIN users u ON p.user_id = u.id WHERE p.parent_post_id IS NULL ORDER BY p.created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var feed []PostFeed
	for rows.Next() {
		var post PostFeed
		var imgURLs pq.StringArray
		if err := rows.Scan(&post.PostID, &post.UserID, &post.Username, &post.ProfileImageURL, &post.Content, &imgURLs, &post.ParentPostID, &post.LikeCount, &post.CreatedAt); err == nil {
			post.ImageURLs = []string(imgURLs)
			feed = append(feed, post)
		}
	}
	return feed, nil
}

func updatePost(postID int, userID int, newContent string) error {
	res, err := db.Exec(`
		UPDATE posts 
		SET content = $1 
		WHERE id = $2 AND user_id = $3`, newContent, postID, userID)
	if err != nil {
		return err
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("post not found or user not authorized to update")
	}
	return nil
}

func deletePost(postID int, userID int) error {
	res, err := db.Exec("DELETE FROM posts WHERE id = $1 AND user_id = $2", postID, userID)
	if err != nil {
		return err
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("post not found or user not authorized to delete")
	}
	return nil
}

// ======================================================================PROFILE POSTS========================================================================//

func GetUserPosts(targetID int, myID int) ([]map[string]interface{}, error) {
	query := `
		SELECT p.id, p.user_id, u.username, COALESCE(u.profile_image_url, ''), p.content, COALESCE(p.image_urls, '{}'), 
		       p.created_at,
		       (SELECT COUNT(*) FROM likes WHERE post_id = p.id) as likes_count,
		       EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $2) as is_liked,
		       (SELECT COUNT(*) FROM reposts WHERE post_id = p.id) as reposts_count,
		       EXISTS(SELECT 1 FROM reposts WHERE post_id = p.id AND user_id = $2) as is_reposted,
		       EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.id AND user_id = $2) as is_bookmarked
		FROM posts p
		JOIN users u ON p.user_id = u.id
		WHERE p.user_id = $1
		ORDER BY p.created_at DESC`
	return fetchPostsByQuery(query, targetID, myID)
}

func GetUserReposts(targetID int, myID int) ([]map[string]interface{}, error) {
	query := `
		SELECT p.id, p.user_id, u.username, COALESCE(u.profile_image_url, ''), p.content, COALESCE(p.image_urls, '{}'), 
		       p.created_at,
		       (SELECT COUNT(*) FROM likes WHERE post_id = p.id) as likes_count,
		       EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $2) as is_liked,
		       (SELECT COUNT(*) FROM reposts WHERE post_id = p.id) as reposts_count,
		       EXISTS(SELECT 1 FROM reposts WHERE post_id = p.id AND user_id = $2) as is_reposted,
		       EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.id AND user_id = $2) as is_bookmarked
		FROM posts p
		JOIN users u ON p.user_id = u.id
		JOIN reposts r ON p.id = r.post_id
		WHERE r.user_id = $1
		ORDER BY r.created_at DESC`
	return fetchPostsByQuery(query, targetID, myID)
}

func GetUserFavorites(targetID int, myID int) ([]map[string]interface{}, error) {
	query := `
		SELECT p.id, p.user_id, u.username, COALESCE(u.profile_image_url, ''), p.content, COALESCE(p.image_urls, '{}'), 
		       p.created_at,
		       (SELECT COUNT(*) FROM likes WHERE post_id = p.id) as likes_count,
		       EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $2) as is_liked,
		       (SELECT COUNT(*) FROM reposts WHERE post_id = p.id) as reposts_count,
		       EXISTS(SELECT 1 FROM reposts WHERE post_id = p.id AND user_id = $2) as is_reposted,
		       EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.id AND user_id = $2) as is_bookmarked
		FROM posts p
		JOIN users u ON p.user_id = u.id
		JOIN likes l ON p.id = l.post_id
		WHERE l.user_id = $1
		ORDER BY l.created_at DESC`
	return fetchPostsByQuery(query, targetID, myID)
}

func GetUserBookmarks(myID int) ([]map[string]interface{}, error) {
	query := `
		SELECT p.id, p.user_id, u.username, COALESCE(u.profile_image_url, ''), p.content, COALESCE(p.image_urls, '{}'), 
		       p.created_at,
		       (SELECT COUNT(*) FROM likes WHERE post_id = p.id) as likes_count,
		       EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $1) as is_liked,
		       (SELECT COUNT(*) FROM reposts WHERE post_id = p.id) as reposts_count,
		       EXISTS(SELECT 1 FROM reposts WHERE post_id = p.id AND user_id = $1) as is_reposted,
		       TRUE as is_bookmarked
		FROM posts p
		JOIN users u ON p.user_id = u.id
		JOIN bookmarks b ON p.id = b.post_id
		WHERE b.user_id = $1
		ORDER BY b.created_at DESC`

	rows, err := db.Query(query, myID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []map[string]interface{}
	for rows.Next() {
		var id, userId, likesCount, repostsCount int
		var username, profileImage, content string
		var imageUrls pq.StringArray
		var createdAt time.Time
		var isLiked, isReposted, isBookmarked bool

		err := rows.Scan(
			&id, &userId, &username, &profileImage, &content,
			&imageUrls, &createdAt,
			&likesCount, &isLiked,
			&repostsCount, &isReposted,
			&isBookmarked,
		)
		if err != nil {
			fmt.Println("❌ GetUserBookmarks Scan Error:", err)
			continue
		}

		posts = append(posts, map[string]interface{}{
			"post_id":           id,
			"user_id":           userId,
			"username":          username,
			"profile_image_url": profileImage,
			"content":           content,
			"image_urls":        []string(imageUrls),
			"created_at":        createdAt,
			"likes_count":       likesCount,
			"is_liked":          isLiked,
			"reposts_count":     repostsCount,
			"is_reposted":       isReposted,
			"is_bookmarked":     isBookmarked,
		})
	}

	if posts == nil {
		posts = []map[string]interface{}{}
	}
	return posts, nil
}

func fetchPostsByQuery(query string, targetID int, myID int) ([]map[string]interface{}, error) {
	rows, err := db.Query(query, targetID, myID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []map[string]interface{}
	for rows.Next() {
		var id, userId, likesCount, repostsCount int
		var username, profileImage, content string
		var imageUrls pq.StringArray
		var createdAt time.Time
		var isLiked, isReposted, isBookmarked bool

		err := rows.Scan(
			&id, &userId, &username, &profileImage, &content,
			&imageUrls, &createdAt,
			&likesCount, &isLiked,
			&repostsCount, &isReposted,
			&isBookmarked,
		)
		if err != nil {
			fmt.Println("❌ fetchPostsByQuery Scan Error:", err)
			continue
		}

		posts = append(posts, map[string]interface{}{
			"post_id":           id,
			"user_id":           userId,
			"username":          username,
			"profile_image_url": profileImage,
			"content":           content,
			"image_urls":        []string(imageUrls),
			"created_at":        createdAt,
			"likes_count":       likesCount,
			"is_liked":          isLiked,
			"reposts_count":     repostsCount,
			"is_reposted":       isReposted,
			"is_bookmarked":     isBookmarked,
		})
	}

	if posts == nil {
		posts = []map[string]interface{}{}
	}
	return posts, nil
}

func getFeedPostsWithUser(userID int) ([]map[string]interface{}, error) {
	query := `
		SELECT 
			p.id, p.user_id, u.username, COALESCE(u.profile_image_url, ''),
			p.content, COALESCE(p.image_urls, '{}'), p.parent_post_id, p.created_at,
			(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as likes_count,
			EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $1) as is_liked,
			(SELECT COUNT(*) FROM reposts WHERE post_id = p.id) as reposts_count,
			EXISTS(SELECT 1 FROM reposts WHERE post_id = p.id AND user_id = $1) as is_reposted,
			EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.id AND user_id = $1) as is_bookmarked
		FROM posts p
		JOIN users u ON p.user_id = u.id
		WHERE p.parent_post_id IS NULL
		ORDER BY p.created_at DESC
		LIMIT 50`
	return fetchPostsWithStatus(query, userID)
}

// ======================================================================LIKE========================================================================//
func toggleLike(userID int, postID int) (bool, error) {
	var isLiked bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM likes WHERE user_id=$1 AND post_id=$2)", userID, postID).Scan(&isLiked)
	if err != nil {
		return false, err
	}

	if isLiked {
		_, err = db.Exec("DELETE FROM likes WHERE user_id=$1 AND post_id=$2", userID, postID)
		if err != nil {
			return true, err
		}
	} else {
		_, err = db.Exec("INSERT INTO likes (user_id, post_id) VALUES ($1, $2)", userID, postID)
		if err != nil {
			return false, err
		}
	}

	var likesCount int
	var repostsCount int
	db.QueryRow("SELECT COUNT(*) FROM likes WHERE post_id=$1", postID).Scan(&likesCount)
	db.QueryRow("SELECT COUNT(*) FROM reposts WHERE post_id=$1", postID).Scan(&repostsCount)

	broadcast(map[string]interface{}{
		"action": "update_post_stats",
		"data": map[string]interface{}{
			"post_id":       postID,
			"likes_count":   likesCount,
			"reposts_count": repostsCount,
		},
	})

	return !isLiked, nil
}

// ======================================================================REPOST========================================================================//
func toggleRepost(userID int, postID int) (bool, error) {
	var isReposted bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM reposts WHERE user_id=$1 AND post_id=$2)", userID, postID).Scan(&isReposted)
	if err != nil {
		return false, err
	}

	if isReposted {
		_, err = db.Exec("DELETE FROM reposts WHERE user_id=$1 AND post_id=$2", userID, postID)
		if err != nil {
			return true, err
		}
	} else {
		_, err = db.Exec("INSERT INTO reposts (user_id, post_id) VALUES ($1, $2)", userID, postID)
		if err != nil {
			return false, err
		}
	}

	var likesCount int
	var repostsCount int
	db.QueryRow("SELECT COUNT(*) FROM likes WHERE post_id=$1", postID).Scan(&likesCount)
	db.QueryRow("SELECT COUNT(*) FROM reposts WHERE post_id=$1", postID).Scan(&repostsCount)

	broadcast(map[string]interface{}{
		"action": "update_post_stats",
		"data": map[string]interface{}{
			"post_id":       postID,
			"likes_count":   likesCount,
			"reposts_count": repostsCount,
		},
	})

	return !isReposted, nil
}

// ======================================================================BOOKMARK========================================================================//
func toggleBookmark(userID int, postID int) (bool, error) {
	var isBookmarked bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM bookmarks WHERE user_id=$1 AND post_id=$2)", userID, postID).Scan(&isBookmarked)
	if err != nil {
		return false, err
	}

	if isBookmarked {
		_, err = db.Exec("DELETE FROM bookmarks WHERE user_id=$1 AND post_id=$2", userID, postID)
		if err != nil {
			return true, err
		}
		return false, nil
	} else {
		_, err = db.Exec("INSERT INTO bookmarks (user_id, post_id) VALUES ($1, $2)", userID, postID)
		if err != nil {
			return false, err
		}
		return true, nil
	}
}

// ======================================================================MESSAGE========================================================================//
func saveMessage(senderID int, receiverID int, content string, imageURL string) (int, error) {
	var imgParam, contentParam interface{}
	if imageURL != "" {
		imgParam = imageURL
	}
	if content != "" {
		contentParam = content
	}
	var newMsgID int
	err := db.QueryRow(`INSERT INTO messages (sender_id, receiver_id, content, image_url) VALUES ($1, $2, $3, $4) RETURNING id`, senderID, receiverID, contentParam, imgParam).Scan(&newMsgID)
	return newMsgID, err
}

func getMessageByID(msgID int) (*Message, error) {
	var msg Message
	err := db.QueryRow(`SELECT id, sender_id, receiver_id, COALESCE(content, ''), image_url, is_read, created_at FROM messages WHERE id = $1`, msgID).Scan(&msg.ID, &msg.SenderID, &msg.ReceiverID, &msg.Content, &msg.ImageURL, &msg.IsRead, &msg.CreatedAt)
	return &msg, err
}

func getChatHistory(user1ID int, user2ID int) ([]Message, error) {
	rows, err := db.Query(`
		SELECT id, sender_id, receiver_id, content, image_url, is_read, created_at 
		FROM messages 
		WHERE (sender_id = $1 AND receiver_id = $2) 
		   OR (sender_id = $2 AND receiver_id = $1)
		ORDER BY created_at ASC`, user1ID, user2ID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []Message
	for rows.Next() {
		var msg Message
		var content, imgURL sql.NullString

		if err := rows.Scan(&msg.ID, &msg.SenderID, &msg.ReceiverID, &content, &imgURL, &msg.IsRead, &msg.CreatedAt); err == nil {

			msg.Content = content.String

			if imgURL.Valid {
				url := imgURL.String
				msg.ImageURL = &url
			} else {
				msg.ImageURL = nil
			}

			history = append(history, msg)
		}
	}

	if history == nil {
		history = []Message{}
	}
	return history, nil
}

// getChatList ดึงรายการแชทล่าสุดของ userID
// แต่ละแถวคือคู่สนทนา พร้อมข้อความล่าสุดและเวลา
func getChatList(userID int) ([]map[string]interface{}, error) {
	rows, err := db.Query(`
		WITH ranked_messages AS (
			SELECT
				m.*,
				CASE WHEN m.sender_id = $1 THEN m.receiver_id ELSE m.sender_id END AS partner_id,
				ROW_NUMBER() OVER (
					PARTITION BY LEAST(m.sender_id, m.receiver_id), GREATEST(m.sender_id, m.receiver_id)
					ORDER BY m.created_at DESC
				) AS rn
			FROM messages m
			WHERE m.sender_id = $1 OR m.receiver_id = $1
		)
		SELECT
			rm.partner_id,
			u.username AS partner_name,
			COALESCE(u.profile_image_url, '') AS profile_image_url,
			COALESCE(rm.content, '') AS last_message,
			rm.created_at
		FROM ranked_messages rm
		JOIN users u ON u.id = rm.partner_id
		WHERE rm.rn = 1
		ORDER BY rm.created_at DESC`, userID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chatList []map[string]interface{}
	for rows.Next() {
		var partnerID int
		var partnerName, profileImage, lastMessage string
		var createdAt time.Time

		if err := rows.Scan(&partnerID, &partnerName, &profileImage, &lastMessage, &createdAt); err == nil {
			chatList = append(chatList, map[string]interface{}{
				"room_id":           partnerID, // ใช้ partner_id เป็น room_id เพื่อเปิดห้องแชท
				"name":              partnerName,
				"profile_image_url": profileImage,
				"message":           lastMessage,
				"time":              createdAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}
	}
	if chatList == nil {
		chatList = []map[string]interface{}{}
	}
	return chatList, nil
}

func deleteMessage(msgID int, senderID int) error {
	res, err := db.Exec("DELETE FROM messages WHERE id = $1 AND sender_id = $2", msgID, senderID)
	if err != nil {
		return err
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("message not found or user not authorized to delete")
	}
	return nil
}

// ======================================================================COMMENT========================================================================//
func getCommentsByPostID(parentID int) ([]PostFeed, error) {
	rows, err := db.Query(`
		SELECT p.id, p.user_id, u.username, COALESCE(u.profile_image_url, ''), 
		       p.content, COALESCE(p.image_urls, '{}'), p.parent_post_id, 
		       (SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count, p.created_at 
		FROM posts p 
		JOIN users u ON p.user_id = u.id 
		WHERE p.parent_post_id = $1 
		ORDER BY p.created_at ASC`, parentID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []PostFeed
	for rows.Next() {
		var post PostFeed
		var imgURLs pq.StringArray
		if err := rows.Scan(&post.PostID, &post.UserID, &post.Username, &post.ProfileImageURL, &post.Content, &imgURLs, &post.ParentPostID, &post.LikeCount, &post.CreatedAt); err == nil {
			post.ImageURLs = []string(imgURLs)
			comments = append(comments, post)
		}
	}
	if comments == nil {
		comments = []PostFeed{}
	}
	return comments, nil
}

// ======================================================================SEARCH========================================================================//
func searchUsersAndPosts(keyword string, userID int) ([]map[string]interface{}, []map[string]interface{}, error) {
	searchQuery := "%" + keyword + "%"

	userRows, err := db.Query(`
		SELECT id, username, COALESCE(profile_image_url, '') 
		FROM users 
		WHERE username ILIKE $1 
		LIMIT 10`, searchQuery)

	var users []map[string]interface{}
	if err == nil {
		defer userRows.Close()
		for userRows.Next() {
			var id int
			var username, profileImage string
			if err := userRows.Scan(&id, &username, &profileImage); err == nil {
				users = append(users, map[string]interface{}{
					"id":                id,
					"username":          username,
					"profile_image_url": profileImage,
				})
			}
		}
	}
	if users == nil {
		users = []map[string]interface{}{}
	}

	postRows, err := db.Query(`
		SELECT 
			p.id, 
			p.user_id, 
			u.username, 
			COALESCE(u.profile_image_url, ''),
			p.content, 
			COALESCE(p.image_urls, '{}'), 
			p.parent_post_id,
			p.created_at,

			(SELECT COUNT(*) FROM likes WHERE post_id = p.id),
			EXISTS(SELECT 1 FROM likes WHERE user_id=$2 AND post_id=p.id),

			(SELECT COUNT(*) FROM reposts WHERE post_id = p.id),
			EXISTS(SELECT 1 FROM reposts WHERE user_id=$2 AND post_id=p.id),

			EXISTS(SELECT 1 FROM bookmarks WHERE user_id=$2 AND post_id=p.id)

		FROM posts p
		JOIN users u ON p.user_id = u.id
		WHERE p.content ILIKE $1
		ORDER BY p.created_at DESC 
		LIMIT 20`, searchQuery, userID)

	var posts []map[string]interface{}
	if err == nil {
		defer postRows.Close()
		for postRows.Next() {
			var id, userId int
			var content, username, profileImage string
			var imageUrls pq.StringArray
			var parentPostId *int
			var createdAt time.Time
			var likesCount, repostsCount int
			var isLiked, isReposted, isBookmarked bool

			err := postRows.Scan(
				&id,
				&userId,
				&username,
				&profileImage,
				&content,
				&imageUrls,
				&parentPostId,
				&createdAt,
				&likesCount,
				&isLiked,
				&repostsCount,
				&isReposted,
				&isBookmarked,
			)

			if err == nil {
				posts = append(posts, map[string]interface{}{
					"post_id":           id,
					"user_id":           userId,
					"username":          username,
					"profile_image_url": profileImage,
					"content":           content,
					"image_urls":        []string(imageUrls),
					"parent_post_id":    parentPostId,
					"created_at":        createdAt,
					"likes_count":       likesCount,
					"is_liked":          isLiked,
					"reposts_count":     repostsCount,
					"is_reposted":       isReposted,
					"is_bookmarked":     isBookmarked,
				})
			} else {
				fmt.Println("Scan error in search:", err)
			}
		}
	}
	if posts == nil {
		posts = []map[string]interface{}{}
	}

	return users, posts, nil
}
