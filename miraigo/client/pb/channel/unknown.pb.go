// Code generated by protoc-gen-golite. DO NOT EDIT.
// source: pb/channel/unknown.proto

package channel

import (
	proto "github.com/RomiChan/protobuf/proto"
)

// see sub_37628C
type ChannelOidb0Xf5BRsp struct {
	GuildId         proto.Option[uint64]     `protobuf:"varint,1,opt"`
	Bots            []*GuildMemberInfo       `protobuf:"bytes,4,rep"`
	Members         []*GuildMemberInfo       `protobuf:"bytes,5,rep"`
	NextIndex       proto.Option[uint32]     `protobuf:"varint,10,opt"`
	Finished        proto.Option[uint32]     `protobuf:"varint,9,opt"`
	NextQueryParam  proto.Option[string]     `protobuf:"bytes,24,opt"`
	MemberWithRoles []*GuildGroupMembersInfo `protobuf:"bytes,25,rep"`
	NextRoleIdIndex proto.Option[uint64]     `protobuf:"varint,26,opt"`
}

type ChannelOidb0Xf88Rsp struct {
	Profile *GuildUserProfile `protobuf:"bytes,1,opt"`
	_       [0]func()
}

type ChannelOidb0Xfc9Rsp struct {
	Profile *GuildUserProfile `protobuf:"bytes,1,opt"`
	_       [0]func()
}

type ChannelOidb0Xf57Rsp struct {
	Rsp *GuildMetaRsp `protobuf:"bytes,1,opt"`
	_   [0]func()
}

type ChannelOidb0Xf55Rsp struct {
	Info *GuildChannelInfo `protobuf:"bytes,1,opt"`
	_    [0]func()
}

type ChannelOidb0Xf5DRsp struct {
	Rsp *ChannelListRsp `protobuf:"bytes,1,opt"`
	_   [0]func()
}

type ChannelOidb0X1017Rsp struct {
	P1 *P10X1017 `protobuf:"bytes,1,opt"`
	_  [0]func()
}

type P10X1017 struct {
	TinyId proto.Option[uint64] `protobuf:"varint,1,opt"`
	Roles  []*GuildUserRole     `protobuf:"bytes,3,rep"`
}

type ChannelOidb0X1019Rsp struct {
	GuildId proto.Option[uint64] `protobuf:"varint,1,opt"`
	Roles   []*GuildRole         `protobuf:"bytes,2,rep"`
}

type ChannelOidb0X1016Rsp struct {
	RoleId proto.Option[uint64] `protobuf:"varint,2,opt"`
	_      [0]func()
}

type GuildMetaRsp struct {
	GuildId proto.Option[uint64] `protobuf:"varint,3,opt"`
	Meta    *GuildMeta           `protobuf:"bytes,4,opt"`
	_       [0]func()
}

type ChannelListRsp struct {
	GuildId  proto.Option[uint64] `protobuf:"varint,1,opt"`
	Channels []*GuildChannelInfo  `protobuf:"bytes,2,rep"` // 5: Category infos
}

type GuildGroupMembersInfo struct {
	RoleId   proto.Option[uint64] `protobuf:"varint,1,opt"`
	Members  []*GuildMemberInfo   `protobuf:"bytes,2,rep"`
	RoleName proto.Option[string] `protobuf:"bytes,3,opt"`
	Color    proto.Option[uint32] `protobuf:"varint,4,opt"`
}

// see sub_374334
type GuildMemberInfo struct {
	Title         proto.Option[string] `protobuf:"bytes,2,opt"`
	Nickname      proto.Option[string] `protobuf:"bytes,3,opt"`
	LastSpeakTime proto.Option[int64]  `protobuf:"varint,4,opt"` // uncertainty
	Role          proto.Option[int32]  `protobuf:"varint,5,opt"` // uncertainty
	TinyId        proto.Option[uint64] `protobuf:"varint,8,opt"`
	_             [0]func()
}

// 频道系统用户资料
type GuildUserProfile struct {
	TinyId    proto.Option[uint64] `protobuf:"varint,2,opt"`
	Nickname  proto.Option[string] `protobuf:"bytes,3,opt"`
	AvatarUrl proto.Option[string] `protobuf:"bytes,6,opt"`
	// 15: avatar url info
	JoinTime proto.Option[int64] `protobuf:"varint,16,opt"` // uncertainty
	_        [0]func()
}

type GuildRole struct {
	RoleId      proto.Option[uint64] `protobuf:"varint,1,opt"`
	Name        proto.Option[string] `protobuf:"bytes,2,opt"`
	ArgbColor   proto.Option[uint32] `protobuf:"varint,3,opt"`
	Independent proto.Option[int32]  `protobuf:"varint,4,opt"`
	Num         proto.Option[int32]  `protobuf:"varint,5,opt"`
	Owned       proto.Option[int32]  `protobuf:"varint,6,opt"` // 是否拥有 存疑
	Disabled    proto.Option[int32]  `protobuf:"varint,7,opt"` // 权限不足或不显示
	MaxNum      proto.Option[int32]  `protobuf:"varint,8,opt"` // 9: ?
	_           [0]func()
}

type GuildUserRole struct {
	RoleId      proto.Option[uint64] `protobuf:"varint,1,opt"`
	Name        proto.Option[string] `protobuf:"bytes,2,opt"`
	ArgbColor   proto.Option[uint32] `protobuf:"varint,3,opt"`
	Independent proto.Option[int32]  `protobuf:"varint,4,opt"`
	_           [0]func()
}

type GuildMeta struct {
	GuildCode      proto.Option[uint64] `protobuf:"varint,2,opt"`
	CreateTime     proto.Option[int64]  `protobuf:"varint,4,opt"`
	MaxMemberCount proto.Option[int64]  `protobuf:"varint,5,opt"`
	MemberCount    proto.Option[int64]  `protobuf:"varint,6,opt"`
	Name           proto.Option[string] `protobuf:"bytes,8,opt"`
	RobotMaxNum    proto.Option[int32]  `protobuf:"varint,11,opt"`
	AdminMaxNum    proto.Option[int32]  `protobuf:"varint,12,opt"`
	Profile        proto.Option[string] `protobuf:"bytes,13,opt"`
	AvatarSeq      proto.Option[int64]  `protobuf:"varint,14,opt"`
	OwnerId        proto.Option[uint64] `protobuf:"varint,18,opt"`
	CoverSeq       proto.Option[int64]  `protobuf:"varint,19,opt"`
	ClientId       proto.Option[int32]  `protobuf:"varint,20,opt"`
	_              [0]func()
}

type GuildChannelInfo struct {
	ChannelId       proto.Option[uint64] `protobuf:"varint,1,opt"`
	ChannelName     proto.Option[string] `protobuf:"bytes,2,opt"`
	CreatorUin      proto.Option[int64]  `protobuf:"varint,3,opt"`
	CreateTime      proto.Option[int64]  `protobuf:"varint,4,opt"`
	GuildId         proto.Option[uint64] `protobuf:"varint,5,opt"`
	FinalNotifyType proto.Option[int32]  `protobuf:"varint,6,opt"`
	ChannelType     proto.Option[int32]  `protobuf:"varint,7,opt"`
	TalkPermission  proto.Option[int32]  `protobuf:"varint,8,opt"`
	// 11 - 14 : MsgInfo
	CreatorTinyId proto.Option[uint64] `protobuf:"varint,15,opt"`
	// 16: Member info ?
	VisibleType        proto.Option[int32]         `protobuf:"varint,22,opt"`
	TopMsg             *GuildChannelTopMsgInfo     `protobuf:"bytes,28,opt"`
	CurrentSlowModeKey proto.Option[int32]         `protobuf:"varint,31,opt"`
	SlowModeInfos      []*GuildChannelSlowModeInfo `protobuf:"bytes,32,rep"`
}

type GuildChannelSlowModeInfo struct {
	SlowModeKey    proto.Option[int32]  `protobuf:"varint,1,opt"`
	SpeakFrequency proto.Option[int32]  `protobuf:"varint,2,opt"`
	SlowModeCircle proto.Option[int32]  `protobuf:"varint,3,opt"`
	SlowModeText   proto.Option[string] `protobuf:"bytes,4,opt"`
	_              [0]func()
}

type GuildChannelTopMsgInfo struct {
	TopMsgSeq            proto.Option[uint64] `protobuf:"varint,1,opt"`
	TopMsgTime           proto.Option[int64]  `protobuf:"varint,2,opt"`
	TopMsgOperatorTinyId proto.Option[uint64] `protobuf:"varint,3,opt"`
	_                    [0]func()
}