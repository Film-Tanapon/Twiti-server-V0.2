package main

import "time"

type User struct {
	ID              int       `json:"id"`
	Email           string    `json:"email"`
	Username        string    `json:"username"`
	ProfileImageURL string    `json:"profile_image_url"`
	CoverImageURL   string    `json:"cover_image_url"`
	Phone           *string   `json:"phone"`
	Country         *string   `json:"country"`
	BirthDate       *string   `json:"birth_date"`
	CreatedAt       time.Time `json:"created_at"`
}

type PostFeed struct {
	PostID          int       `json:"post_id"`
	UserID          int       `json:"user_id"`
	Username        string    `json:"username"`
	ProfileImageURL string    `json:"profile_image_url"`
	Content         string    `json:"content"`
	ImageURLs       []string  `json:"image_urls"`
	ParentPostID    *int      `json:"parent_post_id"`
	LikeCount       int       `json:"like_count"`
	CreatedAt       time.Time `json:"created_at"`
}

type Message struct {
	ID         int       `json:"id"`
	SenderID   int       `json:"sender_id"`
	ReceiverID int       `json:"receiver_id"`
	Content    string    `json:"content"`
	ImageURL   *string   `json:"image_url"`
	IsRead     bool      `json:"is_read"`
	CreatedAt  time.Time `json:"created_at"`
}

type ActionRequest struct {
	Action       string   `json:"action"`
	UserID       int      `json:"user_id"`
	TargetUserID int      `json:"target_user_id,omitempty"`
	ReceiverID   int      `json:"receiver_id,omitempty"`
	PostID       int      `json:"post_id,omitempty"`
	Content      string   `json:"content,omitempty"`
	Query        string   `json:"query,omitempty"`
	ImageURLs    []string `json:"image_urls,omitempty"`
	ImageURL     string   `json:"image_url,omitempty"`
	Token        string   `json:"token,omitempty"`
	Email        string   `json:"email,omitempty"`
	Username     string   `json:"username,omitempty"`
	Password     string   `json:"password,omitempty"`
	OldPassword  string   `json:"old_password,omitempty"`
	OTP          string   `json:"otp,omitempty"`
	Field        string   `json:"field,omitempty"`
	Value        string   `json:"value,omitempty"`
}
