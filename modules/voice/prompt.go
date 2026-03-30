package voice

import "fmt"

const transcribePrompt = `你是一个语音转文字助手。请将音频内容准确转写为文字。
规则：
- 准确还原说话内容，保留原意
- 自动添加标点符号
- 修正明显的口误和重复
- 输出纯文本，不要加任何解释`

const modifyPromptTemplate = `你是一个语音转文字助手。用户已有一段文本，现在通过语音给出修改指令或补充内容。
已有文本：
---
%s
---
请根据音频内容，对已有文本进行修改或补充。
规则：
- 如果语音是修改指令（如"把第一句改成..."），执行修改
- 如果语音是补充内容，追加到已有文本后
- 输出修改后的完整文本，不要加解释`

const chatContextPrefix = `以下是当前聊天的最近对话记录，供你理解专有名词和上下文：
---
%s
---
`

// buildPrompt returns the appropriate prompt based on whether context text and chat context are provided.
func buildPrompt(contextText string, chatContext string) string {
	var prompt string
	if contextText == "" {
		prompt = transcribePrompt
	} else {
		prompt = fmt.Sprintf(modifyPromptTemplate, contextText)
	}
	if chatContext != "" {
		prompt = fmt.Sprintf(chatContextPrefix, chatContext) + prompt
	}
	return prompt
}
