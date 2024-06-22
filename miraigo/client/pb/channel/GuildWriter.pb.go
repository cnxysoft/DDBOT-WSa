// Code generated by protoc-gen-golite. DO NOT EDIT.
// source: pb/channel/GuildWriter.proto

package channel

import (
	proto "github.com/RomiChan/protobuf/proto"
)

type StAlterFeedReq struct {
	ExtInfo          *StCommonExt         `protobuf:"bytes,1,opt"`
	Feed             *StFeed              `protobuf:"bytes,2,opt"`
	BusiReqData      []byte               `protobuf:"bytes,3,opt"`
	MBitmap          proto.Option[uint64] `protobuf:"varint,4,opt"`
	From             proto.Option[int32]  `protobuf:"varint,5,opt"`
	Src              proto.Option[int32]  `protobuf:"varint,6,opt"`
	AlterFeedExtInfo []*CommonEntry       `protobuf:"bytes,7,rep"`
	JsonFeed         proto.Option[string] `protobuf:"bytes,8,opt"`
	ClientContent    *StClientContent     `protobuf:"bytes,9,opt"`
}

type StAlterFeedRsp struct {
	ExtInfo     *StCommonExt `protobuf:"bytes,1,opt"`
	Feed        *StFeed      `protobuf:"bytes,2,opt"`
	BusiRspData []byte       `protobuf:"bytes,3,opt"`
}

type StClientContent struct {
	ClientImageContents []*StClientImageContent `protobuf:"bytes,1,rep"`
	ClientVideoContents []*StClientVideoContent `protobuf:"bytes,2,rep"`
}

type StClientImageContent struct {
	TaskId proto.Option[string] `protobuf:"bytes,1,opt"`
	PicId  proto.Option[string] `protobuf:"bytes,2,opt"`
	Url    proto.Option[string] `protobuf:"bytes,3,opt"`
	_      [0]func()
}

type StClientVideoContent struct {
	TaskId   proto.Option[string] `protobuf:"bytes,1,opt"`
	VideoId  proto.Option[string] `protobuf:"bytes,2,opt"`
	VideoUrl proto.Option[string] `protobuf:"bytes,3,opt"`
	CoverUrl proto.Option[string] `protobuf:"bytes,4,opt"`
	_        [0]func()
}

type StDelFeedReq struct {
	ExtInfo *StCommonExt        `protobuf:"bytes,1,opt"`
	Feed    *StFeed             `protobuf:"bytes,2,opt"`
	From    proto.Option[int32] `protobuf:"varint,3,opt"`
	Src     proto.Option[int32] `protobuf:"varint,4,opt"`
	_       [0]func()
}

type StDelFeedRsp struct {
	ExtInfo *StCommonExt `protobuf:"bytes,1,opt"`
	_       [0]func()
}

type StDoCommentReq struct {
	ExtInfo     *StCommonExt         `protobuf:"bytes,1,opt"`
	CommentType proto.Option[uint32] `protobuf:"varint,2,opt"`
	Comment     *StComment           `protobuf:"bytes,3,opt"`
	Feed        *StFeed              `protobuf:"bytes,4,opt"`
	From        proto.Option[int32]  `protobuf:"varint,5,opt"`
	BusiReqData []byte               `protobuf:"bytes,6,opt"`
	Src         proto.Option[int32]  `protobuf:"varint,7,opt"`
}

type StDoCommentRsp struct {
	ExtInfo     *StCommonExt `protobuf:"bytes,1,opt"`
	Comment     *StComment   `protobuf:"bytes,2,opt"`
	BusiRspData []byte       `protobuf:"bytes,3,opt"`
}

type StDoLikeReq struct {
	ExtInfo         *StCommonExt           `protobuf:"bytes,1,opt"`
	LikeType        proto.Option[uint32]   `protobuf:"varint,2,opt"`
	Like            *StLike                `protobuf:"bytes,3,opt"`
	Feed            *StFeed                `protobuf:"bytes,4,opt"`
	BusiReqData     []byte                 `protobuf:"bytes,5,opt"`
	Comment         *StComment             `protobuf:"bytes,6,opt"`
	Reply           *StReply               `protobuf:"bytes,7,opt"`
	From            proto.Option[int32]    `protobuf:"varint,8,opt"`
	Src             proto.Option[int32]    `protobuf:"varint,9,opt"`
	EmotionReaction *StEmotionReactionInfo `protobuf:"bytes,10,opt"`
}

type StDoLikeRsp struct {
	ExtInfo         *StCommonExt           `protobuf:"bytes,1,opt"`
	Like            *StLike                `protobuf:"bytes,2,opt"`
	BusiRspData     []byte                 `protobuf:"bytes,3,opt"`
	EmotionReaction *StEmotionReactionInfo `protobuf:"bytes,4,opt"`
}

type StDoReplyReq struct {
	ExtInfo     *StCommonExt         `protobuf:"bytes,1,opt"`
	ReplyType   proto.Option[uint32] `protobuf:"varint,2,opt"`
	Reply       *StReply             `protobuf:"bytes,3,opt"`
	Comment     *StComment           `protobuf:"bytes,4,opt"`
	Feed        *StFeed              `protobuf:"bytes,5,opt"`
	From        proto.Option[int32]  `protobuf:"varint,6,opt"`
	BusiReqData []byte               `protobuf:"bytes,7,opt"`
	Src         proto.Option[int32]  `protobuf:"varint,8,opt"`
}

type StDoReplyRsp struct {
	ExtInfo     *StCommonExt `protobuf:"bytes,1,opt"`
	Reply       *StReply     `protobuf:"bytes,2,opt"`
	BusiRspData []byte       `protobuf:"bytes,3,opt"`
}

type StDoSecurityReq struct {
	ExtInfo *StCommonExt        `protobuf:"bytes,1,opt"`
	Feed    *StFeed             `protobuf:"bytes,2,opt"`
	Comment *StComment          `protobuf:"bytes,3,opt"`
	Reply   *StReply            `protobuf:"bytes,4,opt"`
	Poster  *StUser             `protobuf:"bytes,5,opt"`
	SecType proto.Option[int32] `protobuf:"varint,6,opt"`
	_       [0]func()
}

type StDoSecurityRsp struct {
	ExtInfo *StCommonExt `protobuf:"bytes,1,opt"`
	_       [0]func()
}

type StModifyFeedReq struct {
	ExtInfo           *StCommonExt         `protobuf:"bytes,1,opt"`
	Feed              *StFeed              `protobuf:"bytes,2,opt"`
	MBitmap           proto.Option[uint64] `protobuf:"varint,3,opt"`
	From              proto.Option[int32]  `protobuf:"varint,4,opt"`
	Src               proto.Option[int32]  `protobuf:"varint,5,opt"`
	ModifyFeedExtInfo []*CommonEntry       `protobuf:"bytes,6,rep"`
}

type StModifyFeedRsp struct {
	ExtInfo     *StCommonExt `protobuf:"bytes,1,opt"`
	Feed        *StFeed      `protobuf:"bytes,2,opt"`
	BusiRspData []byte       `protobuf:"bytes,3,opt"`
}

type StPublishFeedReq struct {
	ExtInfo          *StCommonExt         `protobuf:"bytes,1,opt"`
	Feed             *StFeed              `protobuf:"bytes,2,opt"`
	BusiReqData      []byte               `protobuf:"bytes,3,opt"`
	From             proto.Option[int32]  `protobuf:"varint,4,opt"`
	Src              proto.Option[int32]  `protobuf:"varint,5,opt"`
	StoreFeedExtInfo []*CommonEntry       `protobuf:"bytes,6,rep"`
	JsonFeed         proto.Option[string] `protobuf:"bytes,7,opt"`
	ClientContent    *StClientContent     `protobuf:"bytes,8,opt"`
}

type StPublishFeedRsp struct {
	ExtInfo     *StCommonExt `protobuf:"bytes,1,opt"`
	Feed        *StFeed      `protobuf:"bytes,2,opt"`
	BusiRspData []byte       `protobuf:"bytes,3,opt"`
}