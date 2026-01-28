package ubot

import (
	"fmt"
	"gotgcalls/third_party/ntgcalls"
	"gotgcalls/third_party/ubot/types"
	"time"

	tg "github.com/amarnathcjd/gogram/telegram"
)

func (ctx *Context) connectCall(chatId int64, mediaDescription ntgcalls.MediaDescription, jsonParams string) error {
	defer func() {
		if ctx.waitConnect[chatId] != nil {
			delete(ctx.waitConnect, chatId)
		}
	}()
	ctx.waitConnect[chatId] = make(chan error)
	if chatId >= 0 {
		defer func() {
			if ctx.p2pConfigs[chatId] != nil {
				delete(ctx.p2pConfigs, chatId)
			}
		}()
		if ctx.p2pConfigs[chatId] == nil {
			p2pConfigs, err := ctx.getP2PConfigs(nil)
			if err != nil {
				return err
			}
			ctx.p2pConfigs[chatId] = p2pConfigs
		}

		err := ctx.binding.CreateP2PCall(chatId)
		if err != nil {
			return err
		}

		err = ctx.binding.SetStreamSources(chatId, ntgcalls.CaptureStream, mediaDescription)
		if err != nil {
			return err
		}

		ctx.p2pConfigs[chatId].GAorB, err = ctx.binding.InitExchange(chatId, ntgcalls.DhConfig{
			G:      ctx.p2pConfigs[chatId].DhConfig.G,
			P:      ctx.p2pConfigs[chatId].DhConfig.P,
			Random: ctx.p2pConfigs[chatId].DhConfig.Random,
		}, ctx.p2pConfigs[chatId].GAorB)
		if err != nil {
			return err
		}

		protocolRaw := ntgcalls.GetProtocol()
		protocol := &tg.PhoneCallProtocol{
			UdpP2P:          protocolRaw.UdpP2P,
			UdpReflector:    protocolRaw.UdpReflector,
			MinLayer:        protocolRaw.MinLayer,
			MaxLayer:        protocolRaw.MaxLayer,
			LibraryVersions: protocolRaw.Versions,
		}

		userId, err := ctx.app.GetSendableUser(chatId)
		if err != nil {
			return err
		}
		if ctx.p2pConfigs[chatId].IsOutgoing {
			callRes, err := ctx.app.PhoneRequestCall(
				&tg.PhoneRequestCallParams{
					Protocol: protocol,
					UserID:   userId,
					GAHash:   ctx.p2pConfigs[chatId].GAorB,
					RandomID: int32(tg.GenRandInt()),
					Video:    mediaDescription.Camera != nil || mediaDescription.Screen != nil,
				},
			)
			if err != nil {
				return err
			}
			// Save call peer immediately so we can discard/cleanup even if updates lag.
			if callRes != nil {
				switch pc := callRes.PhoneCall.(type) {
				case *tg.PhoneCallWaiting:
					ctx.inputCalls[chatId] = &tg.InputPhoneCall{ID: pc.ID, AccessHash: pc.AccessHash}
				case *tg.PhoneCallRequested:
					ctx.inputCalls[chatId] = &tg.InputPhoneCall{ID: pc.ID, AccessHash: pc.AccessHash}
				case *tg.PhoneCallObj:
					ctx.inputCalls[chatId] = &tg.InputPhoneCall{ID: pc.ID, AccessHash: pc.AccessHash}
				}
			}
		} else {
			_, err = ctx.app.PhoneAcceptCall(
				ctx.inputCalls[chatId],
				ctx.p2pConfigs[chatId].GAorB,
				protocol,
			)
			if err != nil {
				return err
			}
		}
		select {
		case err = <-ctx.p2pConfigs[chatId].WaitData:
			if err != nil {
				return err
			}
		case <-time.After(10 * time.Second):
			return fmt.Errorf("timed out waiting for an answer")
		}
		res, err := ctx.binding.ExchangeKeys(
			chatId,
			ctx.p2pConfigs[chatId].GAorB,
			ctx.p2pConfigs[chatId].KeyFingerprint,
		)
		if err != nil {
			return err
		}

		if ctx.p2pConfigs[chatId].IsOutgoing {
			confirmRes, err := ctx.app.PhoneConfirmCall(
				ctx.inputCalls[chatId],
				res.GAOrB,
				res.KeyFingerprint,
				protocol,
			)
			if err != nil {
				return err
			}
			ctx.p2pConfigs[chatId].PhoneCall = confirmRes.PhoneCall.(*tg.PhoneCallObj)
		}

		err = ctx.binding.ConnectP2P(
			chatId,
			parseRTCServers(ctx.p2pConfigs[chatId].PhoneCall.Connections),
			ctx.p2pConfigs[chatId].PhoneCall.Protocol.LibraryVersions,
			ctx.p2pConfigs[chatId].PhoneCall.P2PAllowed,
		)
		if err != nil {
			return err
		}
	} else {
		var err error
		jsonParams, err = ctx.binding.CreateCall(chatId)
		if err != nil {
			_ = ctx.binding.Stop(chatId)
			return err
		}

		err = ctx.binding.SetStreamSources(chatId, ntgcalls.CaptureStream, mediaDescription)
		if err != nil {
			_ = ctx.binding.Stop(chatId)
			return err
		}

		inputGroupCall, err := ctx.getInputGroupCall(chatId)
		if err != nil {
			_ = ctx.binding.Stop(chatId)
			return err
		}

		resultParams := "{\"transport\": null}"
		callResRaw, err := ctx.app.PhoneJoinGroupCall(
			&tg.PhoneJoinGroupCallParams{
				Muted:        false,
				VideoStopped: mediaDescription.Camera == nil,
				Call:         inputGroupCall,
				Params: &tg.DataJson{
					Data: jsonParams,
				},
				JoinAs: &tg.InputPeerUser{
					UserID:     ctx.self.ID,
					AccessHash: ctx.self.AccessHash,
				},
			},
		)
		if err != nil {
			return err
		}
		callRes := callResRaw.(*tg.UpdatesObj)
		for _, update := range callRes.Updates {
			switch update := update.(type) {
			case *tg.UpdateGroupCallConnection:
				resultParams = update.Params.Data
			}
		}

		err = ctx.binding.Connect(
			chatId,
			resultParams,
			false,
		)
		if err != nil {
			return err
		}

		connectionMode, err := ctx.binding.GetConnectionMode(chatId)
		if err != nil {
			return err
		}

		if connectionMode == ntgcalls.StreamConnection && len(jsonParams) > 0 {
			ctx.pendingConnections[chatId] = &types.PendingConnection{
				MediaDescription: mediaDescription,
				Payload:          jsonParams,
			}
		}
	}
	return <-ctx.waitConnect[chatId]
}
