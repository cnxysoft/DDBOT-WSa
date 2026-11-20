package adapter

import "fmt"

// Optional transport capabilities must survive the stable wrapper.
func (r *ReloadableAdapter) GetBotName() string {
	a, done, err := r.acquire()
	if err != nil {
		return ""
	}
	defer done()
	if provider, ok := a.(interface{ GetBotName() string }); ok {
		return provider.GetBotName()
	}
	return ""
}
func (r *ReloadableAdapter) SupportsForward() bool {
	a, done, err := r.acquire()
	if err != nil {
		return false
	}
	defer done()
	if provider, ok := a.(interface{ SupportsForward() bool }); ok {
		return provider.SupportsForward()
	}
	return true
}
func (r *ReloadableAdapter) SendJoinIntro(groupCode int64, message interface{}) (int32, error) {
	a, done, err := r.acquire()
	if err != nil {
		return -1, err
	}
	defer done()
	if sender, ok := a.(interface {
		SendJoinIntro(int64, interface{}) (int32, error)
	}); ok {
		return sender.SendJoinIntro(groupCode, message)
	}
	return a.SendGroupMessage(groupCode, message)
}
func (r *ReloadableAdapter) SetGroupAddRequest(flag string, approve bool, reason string) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	if sender, ok := a.(interface {
		SetGroupAddRequest(string, bool, string) error
	}); ok {
		return sender.SetGroupAddRequest(flag, approve, reason)
	}
	return fmt.Errorf("adapter does not support group requests")
}
func (r *ReloadableAdapter) SetFriendAddRequest(flag string, approve bool) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	if sender, ok := a.(interface{ SetFriendAddRequest(string, bool) error }); ok {
		return sender.SetFriendAddRequest(flag, approve)
	}
	return fmt.Errorf("adapter does not support friend requests")
}
func (r *ReloadableAdapter) MaxMessagePayloadBytes() int {
	a, done, err := r.acquire()
	if err != nil {
		return 0
	}
	defer done()
	if limiter, ok := a.(MessagePayloadLimiter); ok {
		return limiter.MaxMessagePayloadBytes()
	}
	return 0
}
func (r *ReloadableAdapter) GeneratedImageOutputLimitBytes() int {
	a, done, err := r.acquire()
	if err != nil {
		return QQImageOutputLimitBytes
	}
	defer done()
	if limiter, ok := a.(GeneratedImageOutputLimiter); ok {
		return limiter.GeneratedImageOutputLimitBytes()
	}
	return QQImageOutputLimitBytes
}

func (r *ReloadableAdapter) GetAdapterName() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current.adapter == nil {
		return ""
	}
	return r.current.adapter.GetAdapterName()
}
func (r *ReloadableAdapter) GetSelfID() int64 {
	a, done, err := r.acquire()
	if err != nil {
		return 0
	}
	defer done()
	return a.GetSelfID()
}
func (r *ReloadableAdapter) IsConnected() bool {
	a, done, err := r.acquire()
	if err != nil {
		return false
	}
	defer done()
	return a.IsConnected()
}
func (r *ReloadableAdapter) GetFileUrl(groupCode int64, fileId string) string {
	a, done, err := r.acquire()
	if err != nil {
		return ""
	}
	defer done()
	return a.GetFileUrl(groupCode, fileId)
}
func (r *ReloadableAdapter) SendApi(action string, params map[string]interface{}) (interface{}, error) {
	a, done, err := r.acquire()
	if err != nil {
		return nil, err
	}
	defer done()
	return a.SendApi(action, params)
}
func (r *ReloadableAdapter) SendGroupMessage(groupID int64, message interface{}) (int32, error) {
	a, done, err := r.acquire()
	if err != nil {
		return -1, err
	}
	defer done()
	return a.SendGroupMessage(groupID, message)
}
func (r *ReloadableAdapter) SendPrivateMessage(userID int64, message interface{}) (int32, error) {
	a, done, err := r.acquire()
	if err != nil {
		return -1, err
	}
	defer done()
	return a.SendPrivateMessage(userID, message)
}
func (r *ReloadableAdapter) GetGroupList() ([]*GroupInfo, error) {
	a, done, err := r.acquire()
	if err != nil {
		return nil, err
	}
	defer done()
	return a.GetGroupList()
}
func (r *ReloadableAdapter) GetGroupMemberList(groupID int64) ([]*GroupMemberInfo, error) {
	a, done, err := r.acquire()
	if err != nil {
		return nil, err
	}
	defer done()
	return a.GetGroupMemberList(groupID)
}
func (r *ReloadableAdapter) GetFriendList() ([]*FriendInfo, error) {
	a, done, err := r.acquire()
	if err != nil {
		return nil, err
	}
	defer done()
	return a.GetFriendList()
}
func (r *ReloadableAdapter) GetStrangerInfo(userID int64) (map[string]interface{}, error) {
	a, done, err := r.acquire()
	if err != nil {
		return nil, err
	}
	defer done()
	return a.GetStrangerInfo(userID)
}
func (r *ReloadableAdapter) GetGroupInfo(groupID int64) (*GroupInfo, error) {
	a, done, err := r.acquire()
	if err != nil {
		return nil, err
	}
	defer done()
	return a.GetGroupInfo(groupID)
}
func (r *ReloadableAdapter) GetGroupMemberInfo(groupID, userID int64) (*GroupMemberInfo, error) {
	a, done, err := r.acquire()
	if err != nil {
		return nil, err
	}
	defer done()
	return a.GetGroupMemberInfo(groupID, userID)
}
func (r *ReloadableAdapter) DownloadFile(url, base64, name string, headers []string) (string, error) {
	a, done, err := r.acquire()
	if err != nil {
		return "", err
	}
	defer done()
	return a.DownloadFile(url, base64, name, headers)
}
func (r *ReloadableAdapter) GetMsg(msgId int32) (*GetMsgResult, error) {
	a, done, err := r.acquire()
	if err != nil {
		return nil, err
	}
	defer done()
	return a.GetMsg(msgId)
}
func (r *ReloadableAdapter) GetMsgOrg(msgId int32) (interface{}, error) {
	a, done, err := r.acquire()
	if err != nil {
		return nil, err
	}
	defer done()
	return a.GetMsgOrg(msgId)
}
func (r *ReloadableAdapter) RecallMsg(msgId int32) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.RecallMsg(msgId)
}
func (r *ReloadableAdapter) GroupPoke(groupCode, target int64) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.GroupPoke(groupCode, target)
}
func (r *ReloadableAdapter) FriendPoke(target int64) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.FriendPoke(target)
}
func (r *ReloadableAdapter) SetGroupBan(groupCode, memberUin int64, duration int64) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.SetGroupBan(groupCode, memberUin, duration)
}
func (r *ReloadableAdapter) SetGroupWholeBan(groupCode int64, enable bool) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.SetGroupWholeBan(groupCode, enable)
}
func (r *ReloadableAdapter) KickGroupMember(groupCode, memberUin int64, rejectAddRequest bool) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.KickGroupMember(groupCode, memberUin, rejectAddRequest)
}
func (r *ReloadableAdapter) SetGroupLeave(groupCode int64, isDismiss bool) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.SetGroupLeave(groupCode, isDismiss)
}
func (r *ReloadableAdapter) SetGroupAdmin(groupCode, memberUin int64, enable bool) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.SetGroupAdmin(groupCode, memberUin, enable)
}
func (r *ReloadableAdapter) EditGroupCard(groupCode, memberUin int64, card string) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.EditGroupCard(groupCode, memberUin, card)
}
func (r *ReloadableAdapter) EditGroupTitle(groupCode, memberUin int64, title string) error {
	a, done, err := r.acquire()
	if err != nil {
		return err
	}
	defer done()
	return a.EditGroupTitle(groupCode, memberUin, title)
}
func (r *ReloadableAdapter) SendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error) {
	a, done, err := r.acquire()
	if err != nil {
		return -1, "", err
	}
	defer done()
	return a.SendGroupForwardMessage(groupCode, nodes, options)
}
func (r *ReloadableAdapter) SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error) {
	a, done, err := r.acquire()
	if err != nil {
		return -1, "", err
	}
	defer done()
	return a.SendPrivateForwardMessage(userID, nodes, options)
}
