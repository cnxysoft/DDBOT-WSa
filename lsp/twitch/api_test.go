package twitch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidLoginRegex(t *testing.T) {
	validLogins := []string{
		"testuser",
		"test_user",
		"TestUser123",
		"abcd",
		"abcde",
		"abcdeabcdeabcdeabcdeabc", // 25 chars max
	}

	for _, login := range validLogins {
		assert.True(t, validLoginRegex.MatchString(login), "login %q should be valid", login)
	}

	invalidLogins := []string{
		"",
		"abc",  // too short (< 4 chars)
		"a",    // too short
		"test!user",
		"test user",
		"test@user",
		"test.user",
		"a乙",
		"abcdeabcdeabcdeabcdeabcdex", // 26 chars - too long (> 25 chars)
	}

	for _, login := range invalidLogins {
		assert.False(t, validLoginRegex.MatchString(login), "login %q should be invalid", login)
	}
}

func TestBuildLoginQuery(t *testing.T) {
	logins := []string{"user1", "user2", "user3"}
	query := buildLoginQuery(logins)
	assert.Contains(t, query, "user_login=user1")
	assert.Contains(t, query, "user_login=user2")
	assert.Contains(t, query, "user_login=user3")
}

func TestBuildLoginQueryEmpty(t *testing.T) {
	logins := []string{}
	query := buildLoginQuery(logins)
	assert.Equal(t, "", query)
}

func TestBuildLoginQuerySpecialChars(t *testing.T) {
	logins := []string{"user name", "user@test"}
	query := buildLoginQuery(logins)
	assert.Contains(t, query, "user+name")
	assert.Contains(t, query, "user%40test")
}

func TestFormatThumbnailURL(t *testing.T) {
	original := "https://example.com/thumb-{width}x{height}.jpg"
	result := FormatThumbnailURL(original, 1280, 720)
	assert.Equal(t, "https://example.com/thumb-1280x720.jpg", result)
}

func TestFormatThumbnailURLNoPlaceholder(t *testing.T) {
	original := "https://example.com/thumb.jpg"
	result := FormatThumbnailURL(original, 1280, 720)
	assert.Equal(t, original, result)
}
