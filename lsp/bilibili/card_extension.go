package bilibili

import (
	stdjson "encoding/json"
	"errors"
)

var ErrCardTypeMismatch = errors.New("card type mismatch")
var ErrInvalidCardData = errors.New("invalid card data: starts with null byte")

func safeUnmarshalCard(raw string, v interface{}) error {
	b := []byte(raw)
	if len(b) > 0 && b[0] == 0 {
		return ErrInvalidCardData
	}
	return stdjson.Unmarshal(b, v)
}

func (m *Card) GetCardWithImage() (*CardWithImage, error) {
	if m.GetDesc().GetType() == DynamicDescType_WithImage {
		var card = new(CardWithImage)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}

func (m *Card) GetCardWithOrig() (*CardWithOrig, error) {
	if m.GetDesc().GetType() == DynamicDescType_WithOrigin {
		var card = new(CardWithOrig)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}

func (m *Card) GetCardWithVideo() (*CardWithVideo, error) {
	if m.GetDesc().GetType() == DynamicDescType_WithVideo {
		var card = new(CardWithVideo)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}

func (m *Card) GetCardTextOnly() (*CardTextOnly, error) {
	if m.GetDesc().GetType() == DynamicDescType_TextOnly {
		var card = new(CardTextOnly)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}

func (m *Card) GetCardWithPost() (*CardWithPost, error) {
	if m.GetDesc().GetType() == DynamicDescType_WithPost {
		var card = new(CardWithPost)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}

func (m *Card) GetCardWithMusic() (*CardWithMusic, error) {
	if m.GetDesc().GetType() == DynamicDescType_WithMusic {
		var card = new(CardWithMusic)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}

func (m *Card) GetCardWithSketch() (*CardWithSketch, error) {
	if m.GetDesc().GetType() == DynamicDescType_WithSketch {
		var card = new(CardWithSketch)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}

func (m *Card) GetCardWithLive() (*CardWithLive, error) {
	if m.GetDesc().GetType() == DynamicDescType_WithLive {
		var card = new(CardWithLive)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}

func (m *Card) GetCardWithLiveV2() (*CardWithLiveV2, error) {
	if m.GetDesc().GetType() == DynamicDescType_WithLiveV2 {
		var card = new(CardWithLiveV2)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}

func (m *Card) GetCardWithCourse() (*CardWithCourse, error) {
	if m.GetDesc().GetType() == DynamicDescType_WithCourse {
		var card = new(CardWithCourse)
		err := safeUnmarshalCard(m.GetCard(), card)
		return card, err
	}
	return nil, ErrCardTypeMismatch
}
