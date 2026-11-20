package weibo

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractSUBFromCookies(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "M_WEIBOCN_PARAMS", Value: "test1"},
		{Name: "SUB", Value: "test_sub_value"},
		{Name: "_T_WM", Value: "test2"},
	}

	sub := ExtractSUBFromCookies(cookies)
	assert.Equal(t, "test_sub_value", sub)
}

func TestExtractSUBFromCookiesNotFound(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "M_WEIBOCN_PARAMS", Value: "test1"},
		{Name: "_T_WM", Value: "test2"},
	}

	sub := ExtractSUBFromCookies(cookies)
	assert.Equal(t, "", sub)
}

func TestExtractSUBFromCookiesEmpty(t *testing.T) {
	cookies := []*http.Cookie{}

	sub := ExtractSUBFromCookies(cookies)
	assert.Equal(t, "", sub)
}
