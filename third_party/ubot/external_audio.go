package ubot

import "gotgcalls/third_party/ntgcalls"

func (ctx *Context) SendExternalFrame(chatId int64, device ntgcalls.StreamDevice, data []byte, frameData ntgcalls.FrameData) error {
	return ctx.binding.SendExternalFrame(chatId, device, data, frameData)
}
