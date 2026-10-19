package cursor

import "time"

type liveProgress struct {
	start, lastProgress       time.Time
	resumed, content, visible bool
	recoveries, afterVisible  int
	textBytes, thinkingBytes  int
	maxText, maxThinking      int
}

func newLiveProgress(now time.Time, resumed bool) liveProgress {
	return liveProgress{start: now, lastProgress: now, resumed: resumed}
}

func (p *liveProgress) observe(now time.Time, text string, thinking bool) {
	if text == "" {
		return
	}
	p.content = true
	if thinking {
		p.thinkingBytes += len(text)
		if p.thinkingBytes > p.maxThinking {
			p.maxThinking = p.thinkingBytes
			p.lastProgress = now
		}
		return
	}
	p.visible = true
	p.textBytes += len(text)
	if p.textBytes > p.maxText {
		p.maxText = p.textBytes
		p.lastProgress = now
	}
}

func (p liveProgress) budget() time.Duration {
	if p.visible {
		return 45 * time.Second
	}
	if !p.content {
		if p.resumed {
			return 90 * time.Second
		}
		return 180 * time.Second
	}
	if p.resumed {
		return 180 * time.Second
	}
	return 45 * time.Second
}

func (p liveProgress) expired(now time.Time) bool {
	return now.Sub(p.lastProgress) >= p.budget()
}

func (p *liveProgress) recover(now time.Time) (time.Duration, bool) {
	if !p.expired(now) || p.recoveries >= 3 || p.visible && p.afterVisible >= 1 {
		return 0, false
	}
	delay := time.Second << p.recoveries
	p.recoveries++
	if p.visible {
		p.afterVisible++
	}
	p.restart(now)
	return delay, true
}

func (p *liveProgress) restart(now time.Time) {
	p.lastProgress = now
	p.textBytes = 0
	p.thinkingBytes = 0
}
