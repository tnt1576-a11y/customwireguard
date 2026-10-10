package wire

const replayWindowSize = 32768

// ReplayWindow accepts reordered packets within the latest 32768 sequence numbers.
// Its fixed memory use is independent of the number of received packets.
// It is owned by one transport event loop and is not safe for concurrent use.
type ReplayWindow struct {
	high uint64
	bits [replayWindowSize / 64]uint64
}

func (w *ReplayWindow) Accept(seq uint64) (accepted, newest bool) {
	if seq == 0 {
		return false, false
	}
	if seq > w.high {
		if seq-w.high >= replayWindowSize {
			w.bits = [replayWindowSize / 64]uint64{}
		} else {
			for n := w.high + 1; ; n++ {
				i := n % replayWindowSize
				w.bits[i/64] &^= uint64(1) << (i % 64)
				if n == seq {
					break
				}
			}
		}
		w.high = seq
		newest = true
	} else if w.high-seq >= replayWindowSize {
		return false, false
	}
	i := seq % replayWindowSize
	bit := uint64(1) << (i % 64)
	if w.bits[i/64]&bit != 0 {
		return false, false
	}
	w.bits[i/64] |= bit
	return true, newest
}
