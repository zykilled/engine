package engine

import (
	"time"

	"github.com/Monibuca/utils/v3/codec"
)

type AudioPack struct {
	AVPack
	Raw []byte
}
type AudioTrack struct {
	AVTrack
	SoundRate int    //2bit
	SoundSize byte   //1bit
	Channels  byte   //1bit
	ExtraData []byte `json:"-"` //rtmp协议需要先发这个帧

	*AudioPack `json:"-"` // 当前正在写入的音频对象

}

func (at *AudioTrack) writeByteStream() {
	if at.ExtraData != nil {
		switch at.CodecID {
		case codec.CodecID_AAC:
			at.AudioPack.Payload = make([]byte, 2+len(at.Raw))
			at.AudioPack.Payload[0] = at.ExtraData[0]
			at.AudioPack.Payload[1] = 1
			copy(at.AudioPack.Payload[2:], at.Raw)
		default:
			at.AudioPack.Payload = make([]byte, 1+len(at.Raw))
			at.AudioPack.Payload[0] = at.ExtraData[0]
			copy(at.AudioPack.Payload[1:], at.Raw)
		}
	}
}

func (at *AudioTrack) PushByteStream(ts uint32, payload []byte) {

	incomingCodec := payload[0] >> 4
	//若codec变化初始化AvTrack
	if incomingCodec != at.CodecID {
		switch at.CodecID = incomingCodec; at.CodecID {
		case codec.CodecID_AAC:
			if len(payload) < 4 || payload[1] != 0 {
				return
			} else {
				config1, config2 := payload[2], payload[3]
				//audioObjectType = (config1 & 0xF8) >> 3
				// 1 AAC MAIN 	ISO/IEC 14496-3 subpart 4
				// 2 AAC LC 	ISO/IEC 14496-3 subpart 4
				// 3 AAC SSR 	ISO/IEC 14496-3 subpart 4
				// 4 AAC LTP 	ISO/IEC 14496-3 subpart 4
				at.SoundRate = codec.SamplingFrequencies[((config1&0x7)<<1)|(config2>>7)]
				at.Channels = ((config2 >> 3) & 0x0F) //声道
				//frameLengthFlag = (config2 >> 2) & 0x01
				//dependsOnCoreCoder = (config2 >> 1) & 0x01
				//extensionFlag = config2 & 0x01
				at.ExtraData = payload
				at.timebase = time.Duration(at.SoundRate)
				at.Stream.AudioTracks.AddTrack("aac", at)
			}
		default:
			at.SoundRate = codec.SoundRate[(payload[0]&0x0c)>>2] // 采样率 0 = 5.5 kHz or 1 = 11 kHz or 2 = 22 kHz or 3 = 44 kHz
			at.SoundSize = (payload[0] & 0x02) >> 1              // 采样精度 0 = 8-bit samples or 1 = 16-bit samples
			at.Channels = payload[0]&0x01 + 1
			at.ExtraData = payload[:1]
			at.timebase = time.Duration(at.SoundRate)

			switch at.CodecID {
			case codec.CodecID_PCMA:
				at.Stream.AudioTracks.AddTrack("pcma", at)
			case codec.CodecID_PCMU:
				at.Stream.AudioTracks.AddTrack("pcmu", at)
			}
		}
	}

	switch at.CodecID {
	case codec.CodecID_AAC:
		if len(payload) < 3 {
			return
		}
		at.setTS(ts)
		at.AudioPack.Raw = payload[2:]
		at.AudioPack.Payload = payload
		at.push()
	default:
		if len(payload) < 2 {
			return
		}
		at.setTS(ts)
		at.AudioPack.Raw = payload[1:]
		at.AudioPack.Payload = payload
		at.push()
	}
}

func (at *AudioTrack) setCurrent() {
	at.AVTrack.setCurrent()
	at.AudioPack = at.DataItem.Value.(*AudioPack)
}

func (at *AudioTrack) PushRaw(ts uint32, raw []byte) {
	at.setTS(ts)
	at.Raw = raw
	at.push()
}

// Push 来自发布者推送的音频
func (at *AudioTrack) push() {
	if at.Stream != nil {
		at.Stream.Update()
	}
	at.writeByteStream()
	at.addBytes(len(at.Raw))
	at.GetBPS()
	if at.Timestamp.Sub(at.ts) > time.Second {
		at.resetBPS()
	}
	at.AVRing.Step()
	at.setCurrent()
}

func (s *Stream) NewAudioTrack(codecID byte) (at *AudioTrack) {
	at = &AudioTrack{}
	at.timebase = 8000
	at.CodecID = codecID
	at.Stream = s
	at.AVRing.Init(s.Context, defaultRingSize) //ring buffer
	at.AVRing.Do(func(v interface{}) {
		v.(*AVItem).Value = new(AudioPack)
	})
	at.poll = time.Millisecond * 10
	at.setCurrent()
	switch codecID {
	case codec.CodecID_AAC:
		s.AudioTracks.AddTrack("aac", at)
	case codec.CodecID_PCMA:
		s.AudioTracks.AddTrack("pcma", at)
	case codec.CodecID_PCMU:
		s.AudioTracks.AddTrack("pcmu", at)
	}
	return
}
func (at *AudioTrack) SetASC(asc []byte) {
	at.ExtraData = append([]byte{0xAF, 0}, asc...)
	config1 := asc[0]
	config2 := asc[1]
	at.CodecID = 10
	//audioObjectType = (config1 & 0xF8) >> 3
	// 1 AAC MAIN 	ISO/IEC 14496-3 subpart 4
	// 2 AAC LC 	ISO/IEC 14496-3 subpart 4
	// 3 AAC SSR 	ISO/IEC 14496-3 subpart 4
	// 4 AAC LTP 	ISO/IEC 14496-3 subpart 4
	at.SoundRate = codec.SamplingFrequencies[((config1&0x7)<<1)|(config2>>7)]
	at.Channels = (config2 >> 3) & 0x0F //声道
	//frameLengthFlag = (config2 >> 2) & 0x01
	//dependsOnCoreCoder = (config2 >> 1) & 0x01
	//extensionFlag = config2 & 0x01
	at.timebase = time.Duration(at.SoundRate)
	at.Stream.AudioTracks.AddTrack("aac", at)
}

func (at *AudioTrack) Play(onAudio func(uint32, *AudioPack), exit1, exit2 <-chan struct{}) {
	ar := at.Clone()
	item, ap := ar.Read()
	for startTimestamp := item.Timestamp; ; item, ap = ar.Read() {
		select {
		case <-exit1:
			return
		case <-exit2:
			return
		default:
			onAudio(uint32(item.Timestamp.Sub(startTimestamp).Milliseconds()), ap.(*AudioPack))
			ar.MoveNext()
		}
	}
}
