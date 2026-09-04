package session

import (
	"GopherAI/common/aihelper"
	"GopherAI/common/code"
	messagedao "GopherAI/dao/message"
	sessiondao "GopherAI/dao/session"
	memorydomain "GopherAI/internal/memory"
	"GopherAI/model"
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"
)

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

func ensureSessionHelper(requestContext context.Context, userName, sessionID, modelType string, config map[string]interface{}) (*aihelper.AIHelper, error) {
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

	window, err := memorydomain.NewDefaultService().Window(requestContext, userName, sessionID)
	if err != nil {
		return nil, err
	}
	for _, msg := range window.Messages {
		helper.AddMessage(msg.Content, userName, msg.Role == memorydomain.RoleUser, false)
	}
	helper.MarkHistoryLoaded()
	return helper, nil
}

func CreateSessionAndSendMessage(userName string, userQuestion string, modelType string) (string, string, code.Code) {
	return CreateSessionAndSendMessageContext(context.Background(), userName, userQuestion, modelType)
}

func CreateSessionAndSendMessageContext(requestContext context.Context, userName string, userQuestion string, modelType string) (string, string, code.Code) {
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
	helper, err := ensureSessionHelper(requestContext, userName, createdSession.ID, modelType, config)
	if err != nil {
		log.Println("CreateSessionAndSendMessage GetOrCreateAIHelper error:", err)
		return "", "", code.AIModelFail
	}

	//3：生成AI回复
	aiResponse, err_ := helper.GenerateResponse(userName, requestContext, userQuestion)
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

	code_ := StreamMessageToExistingSessionContext(context.Background(), userName, sessionID, userQuestion, modelType, func(msg string) error {
		if _, err := writer.Write([]byte("data: " + msg + "\n\n")); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if code_ != code.CodeSuccess {
		return code_
	}

	_, err := writer.Write([]byte("data: [DONE]\n\n"))
	if err != nil {
		log.Println("StreamMessageToExistingSession write DONE error:", err)
		return code.AIModelFail
	}
	flusher.Flush()

	return code.CodeSuccess
}

func StreamMessageToExistingSessionContext(requestContext context.Context, userName string, sessionID string, userQuestion string, modelType string, emit func(string) error) code.Code {
	config := map[string]interface{}{
		"apiKey":   "your-api-key",
		"username": userName,
	}
	helper, err := ensureSessionHelper(requestContext, userName, sessionID, modelType, config)
	if err != nil {
		log.Println("StreamMessageToExistingSessionContext GetOrCreateAIHelper error:", err)
		return code.AIModelFail
	}

	streamContext, cancel := context.WithCancel(requestContext)
	defer cancel()
	var emitErr error
	callback := func(message string) {
		if emitErr != nil {
			return
		}
		if err := emit(message); err != nil {
			emitErr = err
			cancel()
		}
	}
	if _, err := helper.StreamResponse(userName, streamContext, callback, userQuestion); err != nil {
		if emitErr != nil {
			log.Println("StreamMessageToExistingSessionContext emit error:", emitErr)
			return code.CodeServerBusy
		}
		log.Println("StreamMessageToExistingSessionContext StreamResponse error:", err)
		return code.AIModelFail
	}
	if emitErr != nil {
		return code.CodeServerBusy
	}
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
	return ChatSendContext(context.Background(), userName, sessionID, userQuestion, modelType)
}

func ChatSendContext(requestContext context.Context, userName string, sessionID string, userQuestion string, modelType string) (string, code.Code) {
	config := map[string]interface{}{
		"username": userName, // 用于 RAG 模型获取用户文档（若当前用户选择了RAG模型，该字段将会被用到）
	}
	helper, err := ensureSessionHelper(requestContext, userName, sessionID, modelType, config)
	if err != nil {
		log.Println("ChatSend GetOrCreateAIHelper error:", err)
		return "", code.AIModelFail
	}

	//2：生成AI回复
	aiResponse, err_ := helper.GenerateResponse(userName, requestContext, userQuestion)
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
