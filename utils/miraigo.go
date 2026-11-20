package utils

import (
	"encoding/base64"
	"errors"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/samber/lo"
)

func AdapterMessageFilter(msg []adapter.IMessageElement, filter func(adapter.IMessageElement) bool) []adapter.IMessageElement {
	return lo.Filter(msg, func(e adapter.IMessageElement, _ int) bool {
		return filter(e)
	})
}

func UploadGroupImageByUrl(groupCode int64, url string, isNorm bool) (*adapter.ImageSegment, error) {
	img, err := ImageGet(url)
	if err != nil {
		return nil, err
	}
	return UploadGroupImage(groupCode, img, isNorm)
}

func UploadGroupImage(groupCode int64, img []byte, isNorm bool) (image *adapter.ImageSegment, err error) {
	if isNorm {
		img, err = ImageNormSize(img)
		if err != nil {
			return nil, err
		}
	}
	if !GetBot().IsOnline() {
		return nil, errors.New("bot offline")
	}
	base64Data := base64.StdEncoding.EncodeToString(img)
	return &adapter.ImageSegment{
		File: "base64://" + base64Data,
	}, nil
}

func UploadPrivateImage(uin int64, img []byte, isNorm bool) (*adapter.ImageSegment, error) {
	var err error
	if isNorm {
		img, err = ImageNormSize(img)
		if err != nil {
			return nil, err
		}
	}
	if !GetBot().IsOnline() {
		return nil, errors.New("bot offline")
	}
	base64Data := base64.StdEncoding.EncodeToString(img)
	return &adapter.ImageSegment{
		File: "base64://" + base64Data,
	}, nil
}

const internalMsgTypeGroup = "group"

type internalMsg struct {
	Type          string `json:"type"`
	MsgInfo       string `json:"msg_info"`
	ElementString string `json:"element_string"`
}

func SerializationAdapterGroupMsg(m *adapter.GroupMessage) (string, error) {
	elems := m.Elements
	m.Elements = nil

	defer func() {
		m.Elements = elems
	}()

	mString, err := json.MarshalToString(m)
	if err != nil {
		return "", err
	}

	elemString, err := SerializationAdapterElement(elems)
	if err != nil {
		return "", err
	}

	imsg := &internalMsg{
		Type:          internalMsgTypeGroup,
		MsgInfo:       mString,
		ElementString: elemString,
	}

	return json.MarshalToString(imsg)
}

func DeserializationAdapterGroupMsg(r string) (*adapter.GroupMessage, error) {
	var imsg *internalMsg
	err := json.UnmarshalFromString(r, &imsg)
	if err != nil {
		return nil, err
	}

	var m *adapter.GroupMessage
	err = json.UnmarshalFromString(imsg.MsgInfo, &m)
	if err != nil {
		return nil, err
	}
	elems, err := DeserializationAdapterElement(imsg.ElementString)
	if err != nil {
		return nil, err
	}
	m.Elements = elems
	return m, nil
}

type internalElem struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

const (
	internalTypeText  = "text"
	internalTypeImage = "image"
)

func SerializationAdapterElement(e []adapter.IMessageElement) (string, error) {
	var tmp []*internalElem

	for _, elem := range e {
		switch o := elem.(type) {
		case *adapter.TextSegment:
			b, _ := json.Marshal(o)
			tmp = append(tmp, &internalElem{
				Type:    internalTypeText,
				Content: string(b),
			})
		case *adapter.ImageSegment:
			b, _ := json.Marshal(o)
			tmp = append(tmp, &internalElem{
				Type:    internalTypeImage,
				Content: string(b),
			})
		default:
			panic("unsupported element type")
		}
	}
	return json.MarshalToString(tmp)
}

func DeserializationAdapterElement(r string) ([]adapter.IMessageElement, error) {
	var tmp []*internalElem
	err := json.Unmarshal([]byte(r), &tmp)
	if err != nil {
		return nil, err
	}
	var result []adapter.IMessageElement
	for _, e := range tmp {
		switch e.Type {
		case internalTypeText:
			var elem *adapter.TextSegment
			json.UnmarshalFromString(e.Content, &elem)
			if elem != nil {
				result = append(result, elem)
			}
		case internalTypeImage:
			var elem *adapter.ImageSegment
			json.UnmarshalFromString(e.Content, &elem)
			if elem != nil {
				result = append(result, elem)
			}
		default:
			panic("unsupported element type")
		}
	}
	return result, nil
}
