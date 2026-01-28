package ubot

import tg "github.com/amarnathcjd/gogram/telegram"

func (ctx *Context) Stop(chatId any) error {
	parsedChatId, err := ctx.parseChatId(chatId)
	if err != nil {
		return err
	}
	ctx.presentations = stdRemove(ctx.presentations, parsedChatId)
	delete(ctx.callSources, parsedChatId)
	err = ctx.binding.Stop(parsedChatId)
	if err != nil {
		return err
	}

	// P2P call (user id). Ensure we also discard the Telegram call, otherwise the
	// callee can remain "busy" for the next attempt.
	if parsedChatId >= 0 {
		if peer, ok := ctx.inputCalls[parsedChatId]; ok && peer != nil {
			_, _ = ctx.app.PhoneDiscardCall(&tg.PhoneDiscardCallParams{
				Peer:   peer,
				Reason: &tg.PhoneCallDiscardReasonHangup{},
				// Duration/ConnectionID are not required for hangup here.
			})
		}
		delete(ctx.inputCalls, parsedChatId)
		delete(ctx.p2pConfigs, parsedChatId)
		return nil
	}

	// Group call / presentation (negative chat id in this project).
	if peer, ok := ctx.inputGroupCalls[parsedChatId]; ok && peer != nil {
		_, err = ctx.app.PhoneLeaveGroupCall(peer, 0)
		if err != nil {
			return err
		}
	}
	return nil
}
