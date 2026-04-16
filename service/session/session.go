package session

import (
	"GopherAI/common/aihelper"
	"GopherAI/common/code"
	messagedao "GopherAI/dao/message"
	sessiondao "GopherAI/dao/session"
	"GopherAI/model"
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"
)

var ctx = context.Background()

func GetUserSessionsByUserName(userName string) ([]model.SessionInfo, error) {
	sessions, err := sessiondao.GetSessionsByUserName(userName)
	if err != nil {
		return nil, err
	}

	sessionInfos := make([]model.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		sessionInfos = append(sessionInfos, model.SessionInfo{
			SessionID: sess.ID,
			Title:     sess.Title,
		})
	}

	return sessionInfos, nil
}

func ensureSessionOwnership(userName, sessionID string) error {
	sess, err := sessiondao.GetSessionByID(sessionID)
	if err != nil {
		return err
	}
	if sess.UserName != userName {
		return fmt.Errorf("session %s does not belong to user %s", sessionID, userName)
	}
	return nil
}

func ensureSessionHelper(userName, sessionID, modelType string, config map[string]interface{}) (*aihelper.AIHelper, error) {
	if err := ensureSessionOwnership(userName, sessionID); err != nil {
		return nil, err
	}

	manager := aihelper.GetGlobalManager()
	helper, err := manager.GetOrCreateAIHelper(userName, sessionID, modelType, config)
	if err != nil {
		return nil, err
	}
	if helper.IsHistoryLoaded() {
		return helper, nil
	}

	messages, err := messagedao.GetMessagesBySessionID(sessionID)
	if err != nil {
		return nil, err
	}
	for _, msg := range messages {
		helper.AddMessage(msg.Content, msg.UserName, msg.IsUser, false)
	}
	helper.MarkHistoryLoaded()
	return helper, nil
}

func CreateSessionAndSendMessage(userName string, userQuestion string, modelType string) (string, string, code.Code) {
	//1：创建一个新的会话
	newSession := &model.Session{
		ID:       uuid.New().String(),
		UserName: userName,
		Title:    userQuestion, // 可以根据需求设置标题，这边暂时用用户第一次的问题作为标题
	}
	createdSession, err := sessiondao.CreateSession(newSession)
	if err != nil {
		log.Println("CreateSessionAndSendMessage CreateSession error:", err)
		return "", "", code.CodeServerBusy
	}

	config := map[string]interface{}{
		"apiKey":   "your-api-key", // TODO: 从配置中获取
		"username": userName,       // 用于 RAG 模型获取用户文档
	}
	helper, err := ensureSessionHelper(userName, createdSession.ID, modelType, config)
	if err != nil {
		log.Println("CreateSessionAndSendMessage GetOrCreateAIHelper error:", err)
		return "", "", code.AIModelFail
	}

	//3：生成AI回复
	aiResponse, err_ := helper.GenerateResponse(userName, ctx, userQuestion)
	if err_ != nil {
		log.Println("CreateSessionAndSendMessage GenerateResponse error:", err_)
		return "", "", code.AIModelFail
	}

	return createdSession.ID, aiResponse.Content, code.CodeSuccess
}

func CreateStreamSessionOnly(userName string, userQuestion string) (string, code.Code) {
	newSession := &model.Session{
		ID:       uuid.New().String(),
		UserName: userName,
		Title:    userQuestion,
	}
	createdSession, err := sessiondao.CreateSession(newSession)
	if err != nil {
		log.Println("CreateStreamSessionOnly CreateSession error:", err)
		return "", code.CodeServerBusy
	}
	return createdSession.ID, code.CodeSuccess
}

func StreamMessageToExistingSession(userName string, sessionID string, userQuestion string, modelType string, writer http.ResponseWriter) code.Code {
	// 确保 writer 支持 Flush
	flusher, ok := writer.(http.Flusher)
	if !ok {
		log.Println("StreamMessageToExistingSession: streaming unsupported")
		return code.CodeServerBusy
	}

	config := map[string]interface{}{
		"apiKey":   "your-api-key", // TODO: 从配置中获取
		"username": userName,       // 用于 RAG 模型获取用户文档
	}
	helper, err := ensureSessionHelper(userName, sessionID, modelType, config)
	if err != nil {
		log.Println("StreamMessageToExistingSession GetOrCreateAIHelper error:", err)
		return code.AIModelFail
	}

	cb := func(msg string) {
		// 直接发送数据，不转义
		// SSE 格式：data: <content>\n\n
		log.Printf("[SSE] Sending chunk: %s (len=%d)\n", msg, len(msg))
		_, err := writer.Write([]byte("data: " + msg + "\n\n"))
		if err != nil {
			log.Println("[SSE] Write error:", err)
			return
		}
		flusher.Flush() //  每次必须 flush
		log.Println("[SSE] Flushed")
	}

	_, err_ := helper.StreamResponse(userName, ctx, cb, userQuestion)
	if err_ != nil {
		log.Println("StreamMessageToExistingSession StreamResponse error:", err_)
		return code.AIModelFail
	}

	_, err = writer.Write([]byte("data: [DONE]\n\n"))
	if err != nil {
		log.Println("StreamMessageToExistingSession write DONE error:", err)
		return code.AIModelFail
	}
	flusher.Flush()

	return code.CodeSuccess
}

func CreateStreamSessionAndSendMessage(userName string, userQuestion string, modelType string, writer http.ResponseWriter) (string, code.Code) {

	sessionID, code_ := CreateStreamSessionOnly(userName, userQuestion)
	if code_ != code.CodeSuccess {
		return "", code_
	}

	code_ = StreamMessageToExistingSession(userName, sessionID, userQuestion, modelType, writer)
	if code_ != code.CodeSuccess {

		return sessionID, code_
	}

	return sessionID, code.CodeSuccess
}

func ChatSend(userName string, sessionID string, userQuestion string, modelType string) (string, code.Code) {
	config := map[string]interface{}{
		"username": userName, // 用于 RAG 模型获取用户文档（若当前用户选择了RAG模型，该字段将会被用到）
	}
	helper, err := ensureSessionHelper(userName, sessionID, modelType, config)
	if err != nil {
		log.Println("ChatSend GetOrCreateAIHelper error:", err)
		return "", code.AIModelFail
	}

	//2：生成AI回复
	aiResponse, err_ := helper.GenerateResponse(userName, ctx, userQuestion)
	if err_ != nil {
		log.Println("ChatSend GenerateResponse error:", err_)
		return "", code.AIModelFail
	}

	return aiResponse.Content, code.CodeSuccess
}

func GetChatHistory(userName string, sessionID string) ([]model.History, code.Code) {
	if err := ensureSessionOwnership(userName, sessionID); err != nil {
		log.Println("GetChatHistory ensureSessionOwnership error:", err)
		return nil, code.CodeServerBusy
	}

	messages, err := messagedao.GetMessagesBySessionID(sessionID)
	if err != nil {
		log.Println("GetChatHistory GetMessagesBySessionID error:", err)
		return nil, code.CodeServerBusy
	}

	history := make([]model.History, 0, len(messages))
	for _, msg := range messages {
		history = append(history, model.History{
			IsUser:  msg.IsUser,
			Content: msg.Content,
		})
	}

	return history, code.CodeSuccess
}

func ChatStreamSend(userName string, sessionID string, userQuestion string, modelType string, writer http.ResponseWriter) code.Code {

	return StreamMessageToExistingSession(userName, sessionID, userQuestion, modelType, writer)
}
