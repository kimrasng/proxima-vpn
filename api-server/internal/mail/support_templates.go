package mail

import (
	"fmt"

	"github.com/proximavpn/proxima-vpn/pkg/lang"
)

func SupportReplyEmail(language lang.Code, ticketSubject, link string) (subject, body string) {
	switch language {
	case lang.Korean:
		return "Proxima 문의에 새 답변이 등록되었습니다",
			fmt.Sprintf("안녕하세요.\n\n"+
				"문의하신 \"%s\" 티켓에 운영팀이 답변을 남겼습니다.\n"+
				"아래 링크에서 내용을 확인하고 이어서 대화할 수 있습니다.\n\n"+
				"%s\n\n"+
				"본인이 문의하지 않으셨다면 이 메일을 무시해 주세요.\n", ticketSubject, link)
	case lang.Chinese:
		return "您的 Proxima 工单有新回复",
			fmt.Sprintf("您好，\n\n"+
				"客服团队已回复您的工单“%s”。\n"+
				"请通过下方链接查看并继续沟通。\n\n"+
				"%s\n\n"+
				"如果这不是您本人提交的，请忽略此邮件。\n", ticketSubject, link)
	default:
		return "New reply on your Proxima support ticket",
			fmt.Sprintf("Hello,\n\n"+
				"Our team replied to your ticket \"%s\".\n"+
				"Open the link below to read it and continue the conversation.\n\n"+
				"%s\n\n"+
				"If you did not open this ticket, you can ignore this email.\n", ticketSubject, link)
	}
}
