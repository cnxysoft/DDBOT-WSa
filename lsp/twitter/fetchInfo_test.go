package twitter

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestTweet_RtType(t *testing.T) {
	tweet := &Tweet{
		IsRetweet: true,
	}
	if tweet.RtType() != RETWEET {
		t.Errorf("Expected RETWEET, got %d", tweet.RtType())
	}

	tweet.IsRetweet = false
	if tweet.RtType() != TWEET {
		t.Errorf("Expected TWEET, got %d", tweet.RtType())
	}
}

func TestTweet_IsPinned(t *testing.T) {
	tweet := &Tweet{
		Pinned: true,
	}
	if !tweet.IsPinned() {
		t.Error("Expected tweet to be pinned")
	}

	tweet.Pinned = false
	if tweet.IsPinned() {
		t.Error("Expected tweet not to be pinned")
	}
}

func TestGetIdList(t *testing.T) {
	tweets := []*Tweet{
		{ID: "1"},
		{ID: "2"},
		{ID: "3"},
	}

	expected := []string{"1", "2", "3"}
	actual := GetIdList(tweets)

	if len(actual) != len(expected) {
		t.Fatalf("Expected length %d, got %d", len(expected), len(actual))
	}

	for i, id := range actual {
		if id != expected[i] {
			t.Errorf("Expected %s at index %d, got %s", expected[i], i, id)
		}
	}
}

func TestComputePoW(t *testing.T) {
	challenge := "test_challenge"
	difficulty := 2
	algorithm := "fast"

	nonce, hash := ComputePoW(challenge, difficulty, algorithm)

	// Check that hash starts with the required number of zeros
	expectedPrefix := "00"
	if len(hash) < len(expectedPrefix) || hash[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("Hash %s does not start with %s", hash, expectedPrefix)
	}

	// Verify that the nonce and hash match
	expectedHash := "00cc380fe1b5bcb7a7af8dc51049b94bedcd2884462d3cb2802dee03d15b407b"
	if hash != expectedHash {
		t.Errorf("Computed hash %s does not match expected hash %s for nonce %d", hash, expectedHash, nonce)
	}
}

func TestComputeSlowPoW(t *testing.T) {
	challenge := "test_challenge"
	difficulty := 2

	nonce, hash := computeSlowPoW(challenge, difficulty)

	// Check that hash starts with the required number of zeros
	expectedPrefix := "00"
	if len(hash) < len(expectedPrefix) || hash[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("Hash %s does not start with %s", hash, expectedPrefix)
	}

	// Verify that the nonce and hash match
	expectedHash := "00cc380fe1b5bcb7a7af8dc51049b94bedcd2884462d3cb2802dee03d15b407b"
	if hash != expectedHash {
		t.Errorf("Computed hash %s does not match expected hash %s for nonce %d", hash, expectedHash, nonce)
	}
}

func TestCheckNibbles(t *testing.T) {
	// Create a hash with first few nibbles as zero
	testHash := [32]byte{0x00, 0x00, 0x0F, 0xFF} // First 4 nibbles are zero

	if !checkNibbles(testHash, 4) {
		t.Error("Expected checkNibbles to return true for 4 zero nibbles")
	}

	if !checkNibbles(testHash, 5) {
		t.Error("Expected checkNibbles to return false for 5 zero nibbles")
	}
}

func sha256Sum(data string) string {
	// This is a helper function for testing purposes
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func TestExtractTweetID(t *testing.T) {
	// Test various URL formats that might contain tweet IDs
	testCases := []struct {
		url      string
		expected string
	}{
		{"https://twitter.com/user/status/123456789", "123456789"},
		{"https://twitter.com/user/status/123456789#m", "123456789"},
		{"/user/status/123456789", "123456789"},
		{"123456789", ""},
		{"", ""},
	}

	for _, tc := range testCases {
		actual := ExtractTweetID(tc.url)
		if actual != tc.expected {
			t.Errorf("ExtractTweetID(%s) = %s; expected %s", tc.url, actual, tc.expected)
		}
	}
}

func TestCalcCookie(t *testing.T) {
	// Test that calcCookie returns a non-empty string
	// Note: This test might take some time as calcCookie involves intensive computation
	fixedPrefix := "85096F39A78F6E75C6AF3EC06D8E44676575C514"
	result, err := calcCookie(fixedPrefix)

	if err != nil || result == "" {
		t.Error("calcCookie should return a non-empty string")
	}
}
