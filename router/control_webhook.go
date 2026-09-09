package router

import evaluationcontroller "GopherAI/controller/evaluation"

func evaluationControlWebhookHandler() *evaluationcontroller.ControlWebhookHandler {
	return evaluationcontroller.NewDefaultControlWebhookHandler()
}
