package client

import (
	"encoding/json"
	"math/rand"
	"strconv"
	"time"

	"github.com/pkg/errors"

	"github.com/Mrs4s/MiraiGo/client/pb/msg"
	"github.com/Mrs4s/MiraiGo/internal/proto"
	"github.com/Mrs4s/MiraiGo/message"
)

type WebSocketActionMessagePrivate struct {
	Action string                 `json:"action"`
	Params WebSocketParamsPrivate `json:"params"`
	Echo   string                 `json:"echo,omitempty"`
}

type WebSocketParamsPrivate struct {
	UserID  string `json:"user_id"`
	Message string `json:"message"`
}

// 发送私聊信息
func (c *QQClient) SendPrivateMessage(target int64, m *message.SendingMessage, newstr string) *message.PrivateMessage {
	var messages []MessageContent
	// 检查target是否是由字符串经MD5得到的
	originalUserID, exists := originalStringFromInt64(target)

	// 如果存在于映射中，则使用原始字符串，否则使用当前的target值
	finalUserID := strconv.FormatInt(target, 10)
	if exists {
		finalUserID = originalUserID
	}

	imgCount, videoCount, recordCount, fileCount := 0, 0, 0, 0
	for _, e := range m.Elements {
		var eleType string
		switch e.Type() {
		case message.Image:
			eleType = "image"
			imgCount++
		case message.Video:
			eleType = "video"
			videoCount++
		case message.Voice:
			eleType = "record"
			recordCount++
		case message.File:
			eleType = "file"
			fileCount++
		case message.Text:
			eleType = "text"
		case message.Reply:
			eleType = "reply"
		default:
			logger.Errorf("未知的消息类型")
		}
		messages = append(messages, MessageContent{
			eleType,
			e,
		})
	}
	//判定消息长度限制
	msgLen := message.EstimateLength(m.Elements)
	logger.Infof("本次发送总长: %d, 图片: %d, 视频: %d, 语音：%d, 文件：%d", msgLen, imgCount, videoCount, recordCount, fileCount)
	//判断是否超过最大发送长度
	if msgLen > message.MaxMessageSize || imgCount > 20 {
		return nil
	}
	expTime := 120.00
	tmpMsg := ""
	if len(newstr) > 75 {
		tmpMsg = newstr[:75] + "..."
	} else {
		tmpMsg = newstr
	}
	member := c.FindFriend(target)
	if member == nil {
		member = new(FriendInfo)
		member.Nickname = "临时会话"
		member.Uin = target
		member.client = c
	}
	logger.Infof("发送 私聊消息 给 %s(%v): %s", member.Nickname, finalUserID, tmpMsg)

	data, err := c.SendApi("send_private_msg", map[string]any{
		"user_id": finalUserID,
		"message": messages,
	}, expTime)
	if err != nil {
		if GetSendFailureReminder() {
			c.handleSendFailed(true, newstr, 1, target)
		}
		logger.Errorf("发送私聊消息失败: %v", err)
		return nil
	}
	t, err := json.Marshal(data)
	if err != nil {
		logger.Errorf("解析私聊消息响应失败: %v", err)
		return nil
	}
	var resp ResponseSendMessage
	if err = json.Unmarshal(t, &resp.Data); err != nil {
		logger.Errorf("解析私聊消息响应失败: %v", err)
		return nil
	}
	c.stat.MessageSent.Add(1)
	retMsg := &message.PrivateMessage{
		Id:         resp.Data.MessageID,
		InternalId: int32(rand.Uint32()),
		Self:       c.Uin,
		Target:     target,
		Sender: &message.Sender{
			Uin:      c.Uin,
			Nickname: c.Nickname,
			IsFriend: true,
		},
		Time:     int32(time.Now().Unix()),
		Elements: m.Elements,
	}
	go c.SelfPrivateMessageEvent.dispatch(c, retMsg)
	if GetSendFailureReminder() {
		c.handleSendFailed(false, "", 1, target)
	}
	return retMsg
}

func (c *QQClient) SendGroupTempMessage(groupCode, target int64, m *message.SendingMessage) *message.TempMessage {
	group := c.FindGroup(groupCode)
	if group == nil {
		return nil
	}
	//群临时
	// if c.FindFriend(target) != nil {
	// 	pm := c.SendPrivateMessage(target, m)
	// 	return &message.TempMessage{
	// 		Id:        pm.Id,
	// 		GroupCode: group.Code,
	// 		GroupName: group.Name,
	// 		Self:      c.Uin,
	// 		Sender:    pm.Sender,
	// 		Elements:  m.Elements,
	// 	}
	// }
	mr := int32(rand.Uint32())
	seq := c.nextFriendSeq()
	t := time.Now().Unix()
	_, pkt := c.buildGroupTempSendingPacket(group.Uin, target, seq, mr, t, m)
	_ = c.sendPacket(pkt)
	c.stat.MessageSent.Add(1)
	return &message.TempMessage{
		Id:        seq,
		GroupCode: group.Code,
		GroupName: group.Name,
		Self:      c.Uin,
		Sender: &message.Sender{
			Uin:      c.Uin,
			Nickname: c.Nickname,
			IsFriend: true,
		},
		Elements: m.Elements,
	}
}

func (c *QQClient) sendWPATempMessage(target int64, sig []byte, m *message.SendingMessage) *message.TempMessage {
	mr := int32(rand.Uint32())
	seq := c.nextFriendSeq()
	t := time.Now().Unix()
	_, pkt := c.buildWPATempSendingPacket(target, sig, seq, mr, t, m)
	_ = c.sendPacket(pkt)
	c.stat.MessageSent.Add(1)
	return &message.TempMessage{
		Id:   seq,
		Self: c.Uin,
		Sender: &message.Sender{
			Uin:      c.Uin,
			Nickname: c.Nickname,
			IsFriend: true,
		},
		Elements: m.Elements,
	}
}

func (s *TempSessionInfo) SendMessage(m *message.SendingMessage) (*message.TempMessage, error) {
	switch s.Source {
	case GroupSource:
		return s.client.SendGroupTempMessage(s.GroupCode, s.Sender, m), nil
	case ConsultingSource:
		return s.client.sendWPATempMessage(s.Sender, s.sig, m), nil
	default:
		return nil, errors.New("unsupported message source")
	}
}

/* this function is unused
func (c *QQClient) buildGetOneDayRoamMsgRequest(target, lastMsgTime, random int64, count uint32) (uint16, []byte) {
	seq := c.nextSeq()
	req := &msg.PbGetOneDayRoamMsgReq{
		PeerUin:     proto.Uint64(uint64(target)),
		LastMsgTime: proto.Uint64(uint64(lastMsgTime)),
		Random:      proto.Uint64(uint64(random)),
		ReadCnt:     proto.Some(count),
	}
	payload, _ := proto.Marshal(req)
	packet := packets.BuildUniPacket(c.Uin, seq, "MessageSvc.PbGetOneDayRoamMsg", 1, c.SessionId, EmptyBytes, c.sigInfo.d2Key, payload)
	return seq, packet
}
*/

// MessageSvc.PbSendMsg
func (c *QQClient) buildFriendSendingPacket(target int64, msgSeq, r, pkgNum, pkgIndex, pkgDiv int32, time int64, m []message.IMessageElement) (uint16, []byte) {
	var ptt *msg.Ptt
	if len(m) > 0 {
		if p, ok := m[0].(*message.PrivateVoiceElement); ok {
			ptt = p.Ptt
			m = []message.IMessageElement{}
		}
	}
	req := &msg.SendMessageRequest{
		RoutingHead: &msg.RoutingHead{C2C: &msg.C2C{ToUin: proto.Some(target)}},
		ContentHead: &msg.ContentHead{PkgNum: proto.Some(pkgNum), PkgIndex: proto.Some(pkgIndex), DivSeq: proto.Some(pkgDiv)},
		MsgBody: &msg.MessageBody{
			RichText: &msg.RichText{
				Elems: message.ToProtoElems(m, false),
				Ptt:   ptt,
			},
		},
		MsgSeq:     proto.Some(msgSeq),
		MsgRand:    proto.Some(r),
		SyncCookie: syncCookie(time),
	}
	payload, _ := proto.Marshal(req)
	return c.uniPacket("MessageSvc.PbSendMsg", payload)
}

// MessageSvc.PbSendMsg
func (c *QQClient) buildGroupTempSendingPacket(groupUin, target int64, msgSeq, r int32, time int64, m *message.SendingMessage) (uint16, []byte) {
	req := &msg.SendMessageRequest{
		RoutingHead: &msg.RoutingHead{GrpTmp: &msg.GrpTmp{
			GroupUin: proto.Some(groupUin),
			ToUin:    proto.Some(target),
		}},
		ContentHead: &msg.ContentHead{PkgNum: proto.Int32(1)},
		MsgBody: &msg.MessageBody{
			RichText: &msg.RichText{
				Elems: message.ToProtoElems(m.Elements, false),
			},
		},
		MsgSeq:     proto.Some(msgSeq),
		MsgRand:    proto.Some(r),
		SyncCookie: syncCookie(time),
	}
	payload, _ := proto.Marshal(req)
	return c.uniPacket("MessageSvc.PbSendMsg", payload)
}

func (c *QQClient) buildWPATempSendingPacket(uin int64, sig []byte, msgSeq, r int32, time int64, m *message.SendingMessage) (uint16, []byte) {
	req := &msg.SendMessageRequest{
		RoutingHead: &msg.RoutingHead{WpaTmp: &msg.WPATmp{
			ToUin: proto.Uint64(uint64(uin)),
			Sig:   sig,
		}},
		ContentHead: &msg.ContentHead{PkgNum: proto.Int32(1)},
		MsgBody: &msg.MessageBody{
			RichText: &msg.RichText{
				Elems: message.ToProtoElems(m.Elements, false),
			},
		},
		MsgSeq:     proto.Some(msgSeq),
		MsgRand:    proto.Some(r),
		SyncCookie: syncCookie(time),
	}
	payload, _ := proto.Marshal(req)
	return c.uniPacket("MessageSvc.PbSendMsg", payload)
}

func syncCookie(time int64) []byte {
	cookie, _ := proto.Marshal(&msg.SyncCookie{
		Time:   proto.Some(time),
		Ran1:   proto.Int64(rand.Int63()),
		Ran2:   proto.Int64(rand.Int63()),
		Const1: proto.Some(syncConst1),
		Const2: proto.Some(syncConst2),
		Const3: proto.Int64(0x1d),
	})
	return cookie
}
