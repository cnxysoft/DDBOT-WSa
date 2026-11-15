package adapter

// ElementType represents the type of a message element.
type ElementType int

//go:generate stringer -type ElementType -linecomment
const (
	ElementTypeText            ElementType = iota // 文本
	ElementTypeImage                              // 图片
	ElementTypeFace                               // 表情
	ElementTypeAt                                 // 艾特
	ElementTypeReply                              // 回复
	ElementTypeService                            // 服务
	ElementTypeForward                            // 转发
	ElementTypeFile                               // 文件
	ElementTypeVoice                              // 语音
	ElementTypeVideo                              // 视频
	ElementTypeLightApp                           // 轻应用
	ElementTypeRedBag                             // 红包
	ElementTypeDice                               // 骰子
	ElementTypeFingerGuessing                     // 猜拳
	ElementTypeMarketFace                         // 表情包
	ElementTypeAnimatedSticker                    // 动态表情
)

// IMessageElement is the core message element interface.
type IMessageElement interface {
	Type() ElementType
	ToSendingMessage() *SendingMessage
}

// SendingMessage is a message ready for sending.
type SendingMessage struct {
	Elements []IMessageElement
}

func (s *SendingMessage) FirstOrNil(predicate func(IMessageElement) bool) IMessageElement {
	for _, e := range s.Elements {
		if predicate(e) {
			return e
		}
	}
	return nil
}

// Append appends elements to the sending message.
func (s *SendingMessage) Append(elems ...IMessageElement) {
	s.Elements = append(s.Elements, elems...)
}

// region segment types

// TextSegment represents a text message element.
type TextSegment struct {
	Content string
}

func (s *TextSegment) Type() ElementType { return ElementTypeText }
func (s *TextSegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// ImageSegment represents an image message element.
type ImageSegment struct {
	File string // file path, url, or base64://...
	Url  string
	// Generated images carry a local-only flag for pre-send budget validation.
	Generated bool `json:"-"`
}

func (s *ImageSegment) Type() ElementType { return ElementTypeImage }
func (s *ImageSegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// FaceSegment represents a face (emoji) message element.
type FaceSegment struct {
	Index int32
	Name  string
}

func (s *FaceSegment) Type() ElementType { return ElementTypeFace }
func (s *FaceSegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// AtSegment represents an @ mention message element.
type AtSegment struct {
	Target  int64
	Display string
}

func (s *AtSegment) Type() ElementType { return ElementTypeAt }
func (s *AtSegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// ReplySegment represents a reply message element.
type ReplySegment struct {
	ReplySeq int32
	Id       string
	Sender   int64
	GroupID  int64
	Time     int32
}

func (s *ReplySegment) Type() ElementType { return ElementTypeReply }
func (s *ReplySegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// VoiceSegment represents a voice message element.
type VoiceSegment struct {
	Name string
	Md5  []byte
	Size int32
	Url  string
	Data []byte
}

func (s *VoiceSegment) Type() ElementType { return ElementTypeVoice }
func (s *VoiceSegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// VideoSegment represents a video message element.
type VideoSegment struct {
	Name      string
	Uuid      []byte
	Size      int32
	ThumbSize int32
	Md5       []byte
	ThumbMd5  []byte
	Url       string
}

func (s *VideoSegment) Type() ElementType { return ElementTypeVideo }
func (s *VideoSegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// FileSegment represents a file message element.
type FileSegment struct {
	Name  string
	Size  int64
	Path  string
	Id    string
	Url   string
	Busid int32
}

func (s *FileSegment) Type() ElementType { return ElementTypeFile }
func (s *FileSegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// ForwardSegment represents a forward message element.
type ForwardSegment struct {
	ResId string
}

func (s *ForwardSegment) Type() ElementType { return ElementTypeForward }
func (s *ForwardSegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// JsonSegment represents a JSON/app message element.
type JsonSegment struct {
	Content string
}

func (s *JsonSegment) Type() ElementType { return ElementTypeService }
func (s *JsonSegment) ToSendingMessage() *SendingMessage {
	return &SendingMessage{Elements: []IMessageElement{s}}
}

// MarketFaceSegment represents a market face message element.
type MarketFaceSegment struct {
	Id   string
	Name string
}

// DiceSegment represents a dice message element.
type DiceSegment struct {
	Value int32
}

// FingerGuessingSegment represents a finger guessing message element.
type FingerGuessingSegment struct {
	Value int32
}

// endregion

// region message types

// GroupMessage represents a group message event.
type GroupMessage struct {
	ID        int64
	GroupCode int64
	GroupName string
	Sender    *SenderInfo
	Time      int64
	Elements  []IMessageElement
}

// PrivateMessage represents a private message event.
type PrivateMessage struct {
	ID       int64
	UserID   int64
	Self     int64
	Sender   *SenderInfo
	Time     int64
	Elements []IMessageElement
}

// endregion
