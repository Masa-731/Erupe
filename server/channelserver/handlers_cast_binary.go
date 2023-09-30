package channelserver

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/binpacket"
	"erupe-ce/network/mhfpacket"
)

// MSG_SYS_CAST[ED]_BINARY types enum
const (
	BinaryMessageTypeState      = 0
	BinaryMessageTypeChat       = 1
	BinaryMessageTypeData       = 3
	BinaryMessageTypeMailNotify = 4
	BinaryMessageTypeEmote      = 6
)

// MSG_SYS_CAST[ED]_BINARY broadcast types enum
const (
	BroadcastTypeTargeted = 0x01
	BroadcastTypeStage    = 0x03
	BroadcastTypeServer   = 0x06
	BroadcastTypeWorld    = 0x0a
)

func sendServerChatMessage(s *Session, message string) {
	// Make the inside of the casted binary
	bf := byteframe.NewByteFrame()
	bf.SetLE()
	msgBinChat := &binpacket.MsgBinChat{
		Unk0:       0,
		Type:       5,
		Flags:      0x80,
		Message:    message,
		SenderName: "Erupe",
	}
	msgBinChat.Build(bf)

	castedBin := &mhfpacket.MsgSysCastedBinary{
		CharID:         s.charID,
		MessageType:    BinaryMessageTypeChat,
		RawDataPayload: bf.Data(),
	}

	s.QueueSendMHF(castedBin)
}

func handleMsgSysCastBinary(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysCastBinary)
	tmp := byteframe.NewByteFrameFromBytes(pkt.RawDataPayload)

	if pkt.BroadcastType == 0x03 && pkt.MessageType == 0x03 && len(pkt.RawDataPayload) == 0x10 {
		if tmp.ReadUint16() == 0x0002 && tmp.ReadUint8() == 0x18 {
			_ = tmp.ReadBytes(9)
			tmp.SetLE()
			frame := tmp.ReadUint32()
			sendServerChatMessage(s, fmt.Sprintf("TIME : %d'%d.%03d (%dframe)", frame/30/60, frame/30%60, int(math.Round(float64(frame%30*100)/3)), frame))
		}
	}

	// Parse out the real casted binary payload
	var msgBinTargeted *binpacket.MsgBinTargeted
	var authorLen, msgLen uint16
	var msg []byte

	isDiceCommand := false
	if pkt.MessageType == BinaryMessageTypeChat {
		tmp.SetLE()
		tmp.Seek(int64(0), 0)
		_ = tmp.ReadUint32()
		authorLen = tmp.ReadUint16()
		msgLen = tmp.ReadUint16()
		msg = tmp.ReadNullTerminatedBytes()
	}

	// Customise payload
	realPayload := pkt.RawDataPayload
	if pkt.BroadcastType == BroadcastTypeTargeted {
		tmp.SetBE()
		tmp.Seek(int64(0), 0)
		msgBinTargeted = &binpacket.MsgBinTargeted{}
		err := msgBinTargeted.Parse(tmp)
		if err != nil {
			s.logger.Warn("Failed to parse targeted cast binary")
			return
		}
		realPayload = msgBinTargeted.RawDataPayload
	} else if pkt.MessageType == BinaryMessageTypeChat {
		if msgLen == 6 && string(msg) == "@dice" {
			isDiceCommand = true
			roll := byteframe.NewByteFrame()
			roll.WriteInt16(1) // Unk
			roll.SetLE()
			roll.WriteUint16(4) // Unk
			roll.WriteUint16(authorLen)
			rand.Seed(time.Now().UnixNano())
			dice := fmt.Sprintf("%d", rand.Intn(100)+1)
			roll.WriteUint16(uint16(len(dice) + 1))
			roll.WriteNullTerminatedBytes([]byte(dice))
			roll.WriteNullTerminatedBytes(tmp.ReadNullTerminatedBytes())
			realPayload = roll.Data()
		}
	}

	// Make the response to forward to the other client(s).
	resp := &mhfpacket.MsgSysCastedBinary{
		CharID:         s.charID,
		BroadcastType:  pkt.BroadcastType, // (The client never uses Type0 upon receiving)
		MessageType:    pkt.MessageType,
		RawDataPayload: realPayload,
	}

	// Send to the proper recipients.
	switch pkt.BroadcastType {
	case BroadcastTypeWorld:
		s.server.WorldcastMHF(resp, s, nil)
	case BroadcastTypeStage:
		if isDiceCommand {
			s.stage.BroadcastMHF(resp, nil) // send dice result back to caller
		} else {
			s.stage.BroadcastMHF(resp, s)
		}
	case BroadcastTypeServer:
		if pkt.MessageType == 1 {
			raviSema := getRaviSemaphore(s)
			if raviSema != "" {
				s.server.BroadcastMHF(resp, s)
			}
		} else {
			s.server.BroadcastMHF(resp, s)
		}
	case BroadcastTypeTargeted:
		for _, targetID := range (*msgBinTargeted).TargetCharIDs {
			char := s.server.FindSessionByCharID(targetID)

			if char != nil {
				char.QueueSendMHF(resp)
			}
		}
	default:
		s.Lock()
		haveStage := s.stage != nil
		if haveStage {
			s.stage.BroadcastMHF(resp, s)
		}
		s.Unlock()
	}

	// Handle chat
	if pkt.MessageType == BinaryMessageTypeChat {
		bf := byteframe.NewByteFrameFromBytes(realPayload)

		// IMPORTANT! Casted binary objects are sent _as they are in memory_,
		// this means little endian for LE CPUs, might be different for PS3/PS4/PSP/XBOX.
		bf.SetLE()

		chatMessage := &binpacket.MsgBinChat{}
		chatMessage.Parse(bf)

		fmt.Printf("Got chat message: %+v\n", chatMessage)

		// Flush all objects and users and reload
		if strings.HasPrefix(chatMessage.Message, "!reload") {
			sendServerChatMessage(s, "Reloading players...")
			var temp mhfpacket.MHFPacket
			deleteNotif := byteframe.NewByteFrame()
			for _, object := range s.stage.objects {
				if object.ownerCharID == s.charID {
					continue
				}
				temp = &mhfpacket.MsgSysDeleteObject{ObjID: object.id}
				deleteNotif.WriteUint16(uint16(temp.Opcode()))
				temp.Build(deleteNotif, s.clientContext)
			}
			for _, session := range s.server.sessions {
				if s == session {
					continue
				}
				temp = &mhfpacket.MsgSysDeleteUser{CharID: session.charID}
				deleteNotif.WriteUint16(uint16(temp.Opcode()))
				temp.Build(deleteNotif, s.clientContext)
			}
			deleteNotif.WriteUint16(0x0010)
			s.QueueSend(deleteNotif.Data())
			time.Sleep(500 * time.Millisecond)
			reloadNotif := byteframe.NewByteFrame()
			for _, session := range s.server.sessions {
				if s == session {
					continue
				}
				temp = &mhfpacket.MsgSysInsertUser{CharID: session.charID}
				reloadNotif.WriteUint16(uint16(temp.Opcode()))
				temp.Build(reloadNotif, s.clientContext)
				for i := 0; i < 3; i++ {
					temp = &mhfpacket.MsgSysNotifyUserBinary{
						CharID:     session.charID,
						BinaryType: uint8(i + 1),
					}
					reloadNotif.WriteUint16(uint16(temp.Opcode()))
					temp.Build(reloadNotif, s.clientContext)
				}
			}
			for _, obj := range s.stage.objects {
				if obj.ownerCharID == s.charID {
					continue
				}
				temp = &mhfpacket.MsgSysDuplicateObject{
					ObjID:       obj.id,
					X:           obj.x,
					Y:           obj.y,
					Z:           obj.z,
					Unk0:        0,
					OwnerCharID: obj.ownerCharID,
				}
				reloadNotif.WriteUint16(uint16(temp.Opcode()))
				temp.Build(reloadNotif, s.clientContext)
			}
			reloadNotif.WriteUint16(0x0010)
			s.QueueSend(reloadNotif.Data())
		}

		// Set account rights
		if strings.HasPrefix(chatMessage.Message, "!rights") {
			var v uint32
			n, err := fmt.Sscanf(chatMessage.Message, "!rights %d", &v)
			if err != nil || n != 1 {
				sendServerChatMessage(s, "Error in command. Format: !rights n")
			} else {
				_, err = s.server.db.Exec("UPDATE users u SET rights=$1 WHERE u.id=(SELECT c.user_id FROM characters c WHERE c.id=$2)", v, s.charID)
				if err == nil {
					sendServerChatMessage(s, fmt.Sprintf("Set rights integer: %d", v))
				}
			}
		}

		if strings.HasPrefix(chatMessage.Message, "!effect") {
			var e1, e2, e3, e4 uint8
			n, err := fmt.Sscanf(chatMessage.Message, "!effect %d %d %d %d", &e1, &e2, &e3, &e4)
			if err != nil || n != 4 {
				sendServerChatMessage(s, "コマンドが無効です。使用例：!effect num1 num2 num3 num4")
			} else if 7 < e1+e2+e3+e4 {
				sendServerChatMessage(s, "レベルの合計値は7以下になるように入力してください。")
			} else {
				_, err = s.server.db.Exec("UPDATE demo_color SET color_1 = $1, color_2 = $2, color_3 = $3, color_4 = $4 WHERE char_id = $5", e1, e2, e3, e4, s.charID)
				if err == nil {
					sendServerChatMessage(s, fmt.Sprintf("祈珠のレベルを以下の値で設定しました：赤 %d 黄 %d 緑 %d 青 %d", e1, e2, e3, e4))
				}
			}
		}

		if strings.HasPrefix(chatMessage.Message, "!song") {
			var s1, s2, s3, s4 uint8
			n, err := fmt.Sscanf(chatMessage.Message, "!song %d %d %d %d", &s1, &s2, &s3, &s4)
			if err != nil || n != 4 {
				sendServerChatMessage(s, "コマンドが無効です。使用例：!song num1 num2 num3 num4")
			} else if 25 < s1 || 25 < s2 || 25 < s3 || 25 < s4 {
				sendServerChatMessage(s, "祈珠スキルIDは1-25の値で設定してください。")
			} else {
				_, err = s.server.db.Exec("UPDATE demo_select_kiju SET effect1 = $1, effect2 = $2, effect3 = $3, effect4 = $4 WHERE char_id = $5", s1, s2, s3, s4, s.charID)
				if err == nil {
					sendServerChatMessage(s, fmt.Sprintf("祈珠スキルを以下のIDで設定しました：赤 %d 黄 %d 緑 %d 青 %d", s1, s2, s3, s4))
				}
			}
		}

		// Discord integration
		if (pkt.BroadcastType == BroadcastTypeStage && s.stage.id == "sl1Ns200p0a0u0") || pkt.BroadcastType == BroadcastTypeWorld {
			s.server.DiscordChannelSend(chatMessage.SenderName, chatMessage.Message)
		}

		// RAVI COMMANDS V2
		if strings.HasPrefix(chatMessage.Message, "!ravi") {
			if getRaviSemaphore(s) != "" {
				s.server.raviente.Lock()
				if !strings.HasPrefix(chatMessage.Message, "!ravi ") {
					sendServerChatMessage(s, "No Raviente command specified!")
				} else {
					if strings.HasPrefix(chatMessage.Message, "!ravi start") {
						if s.server.raviente.register.startTime == 0 {
							s.server.raviente.register.startTime = s.server.raviente.register.postTime
							sendServerChatMessage(s, "The Great Slaying will begin in a moment")
							s.notifyRavi()
						} else {
							sendServerChatMessage(s, "The Great Slaying has already begun!")
						}
					} else if strings.HasPrefix(chatMessage.Message, "!ravi sm") || strings.HasPrefix(chatMessage.Message, "!ravi setmultiplier") {
						var num uint16
						n, numerr := fmt.Sscanf(chatMessage.Message, "!ravi sm %d", &num)
						if numerr != nil || n != 1 {
							sendServerChatMessage(s, "Error in command. Format: !ravi sm n")
						} else if s.server.raviente.state.damageMultiplier == 1 {
							if num > 32 {
								sendServerChatMessage(s, "Raviente multiplier too high, defaulting to 32x")
								s.server.raviente.state.damageMultiplier = 32
							} else {
								sendServerChatMessage(s, fmt.Sprintf("Raviente multiplier set to %dx", num))
								s.server.raviente.state.damageMultiplier = uint32(num)
							}
						} else {
							sendServerChatMessage(s, fmt.Sprintf("Raviente multiplier is already set to %dx!", s.server.raviente.state.damageMultiplier))
						}
					} else if strings.HasPrefix(chatMessage.Message, "!ravi cm") || strings.HasPrefix(chatMessage.Message, "!ravi checkmultiplier") {
						sendServerChatMessage(s, fmt.Sprintf("Raviente multiplier is currently %dx", s.server.raviente.state.damageMultiplier))
					} else if strings.HasPrefix(chatMessage.Message, "!ravi sr") || strings.HasPrefix(chatMessage.Message, "!ravi sendres") {
						if s.server.raviente.state.stateData[28] > 0 {
							sendServerChatMessage(s, "Sending resurrection support!")
							s.server.raviente.state.stateData[28] = 0
						} else {
							sendServerChatMessage(s, "Resurrection support has not been requested!")
						}
					} else if strings.HasPrefix(chatMessage.Message, "!ravi ss") || strings.HasPrefix(chatMessage.Message, "!ravi sendsed") {
						sendServerChatMessage(s, "Sending sedation support if requested!")
						// Total BerRavi HP
						HP := s.server.raviente.state.stateData[0] + s.server.raviente.state.stateData[1] + s.server.raviente.state.stateData[2] + s.server.raviente.state.stateData[3] + s.server.raviente.state.stateData[4]
						s.server.raviente.support.supportData[1] = HP
					} else if strings.HasPrefix(chatMessage.Message, "!ravi rs") || strings.HasPrefix(chatMessage.Message, "!ravi reqsed") {
						sendServerChatMessage(s, "Requesting sedation support!")
						// Total BerRavi HP
						HP := s.server.raviente.state.stateData[0] + s.server.raviente.state.stateData[1] + s.server.raviente.state.stateData[2] + s.server.raviente.state.stateData[3] + s.server.raviente.state.stateData[4]
						s.server.raviente.support.supportData[1] = HP + 12
					} else {
						sendServerChatMessage(s, "Raviente command not recognised!")
					}
				}
				s.server.raviente.Unlock()
			} else {
				sendServerChatMessage(s, "No one has joined the Great Slaying!")
			}
		}
		// END RAVI COMMANDS V2

		if strings.HasPrefix(chatMessage.Message, "!tele ") {
			var x, y int16
			n, err := fmt.Sscanf(chatMessage.Message, "!tele %d %d", &x, &y)
			if err != nil || n != 2 {
				sendServerChatMessage(s, "Invalid command. Usage:\"!tele 500 500\"")
			} else {
				sendServerChatMessage(s, fmt.Sprintf("Teleporting to %d %d", x, y))

				// Make the inside of the casted binary
				payload := byteframe.NewByteFrame()
				payload.SetLE()
				payload.WriteUint8(2) // SetState type(position == 2)
				payload.WriteInt16(x) // X
				payload.WriteInt16(y) // Y
				payloadBytes := payload.Data()

				s.QueueSendMHF(&mhfpacket.MsgSysCastedBinary{
					CharID:         s.charID,
					MessageType:    BinaryMessageTypeState,
					RawDataPayload: payloadBytes,
				})
			}
		}
	}
}

func handleMsgSysCastedBinary(s *Session, p mhfpacket.MHFPacket) {}
