package memo

import (
	"container/list"
	"sync"
	"time"
)

type Key interface {}

type Value interface {}

type Action int

// should not do long time run
type Exchanger func(key Key, new, eld Value, release bool)

type Table struct {
	keepLiveDuration time.Duration
	maxEntry          int
	rw                sync.RWMutex
	ll                *list.List
	exchanger         Exchanger
	mp                map[Key]*list.Element
	seq               uint64
}

func NewTable(maxEntry int, keepLiveDuration time.Duration, exchanger Exchanger) *Table {
	if exchanger == nil {
		exchanger = func(key Key, new, eld Value, release bool){}
	}

	tb := &Table{
		maxEntry: maxEntry,
		keepLiveDuration: keepLiveDuration,
		exchanger: exchanger,
	}
	tb.Clear()
	return tb
}


func (tb *Table) Clear() {
	tb.rw.Lock()
	defer tb.rw.Unlock()

	if tb.ll != nil {
		f := tb.ll.Front()
		for ; f != nil ; f = f.Next() {
			ctn := f.Value.(*container)
			ctn.release(tb.exchanger, ctn.GetSeq())
		}
	}
	tb.mp = map[Key]*list.Element{}
	tb.ll = list.New()
}

func (tb *Table) Keys() []Key {
	now := time.Now()
	tb.rw.RLock()
	defer tb.rw.RUnlock()

	var ks = make([]Key, 0, len(tb.mp))
	for key, v := range tb.mp {
		if !v.Value.(*container).IsTimeout(now) {
			ks = append(ks, key)
		}
	}
	return ks
}

func (tb *Table) Get(key interface{}) (interface{}, bool) {
	now := time.Now()
	tb.rw.Lock()
	e := tb.mp[key]
	if e == nil {
		tb.rw.Unlock()
		return nil, false
	}
	ctn := e.Value.(*container)
	seq := ctn.seq
	tb.moveToEnd(e)
	tb.rw.Unlock()
	return ctn.GetData(now, seq)
}

func (tb *Table) Fetch(key Key, def Value) (value Value, loaded bool) {
	var (
		ctn *container
		now = time.Now()
		ok bool
	)
	for {
		tb.rw.Lock()
		ctn, loaded = tb.load(key, def, tb.keepLiveDuration)
		seq := ctn.GetSeq()
		tb.rw.Unlock()
		if loaded {
			value, ok = ctn.GetData(now, seq)
			if !ok {
				continue
			}
		} else {
			value = def
		}
		break
	}
	return
}

func (tb *Table) Set(key, value Value, option ...SetOption) {
	o := mergeSetOption(option)
	keepLiveDuration := tb.keepLiveDuration
	if o.KeepLiveDuration != nil {
		keepLiveDuration = *o.KeepLiveDuration
	}

	tb.rw.Lock()
	ctn, loaded := tb.load(key, value, keepLiveDuration)
	seq := ctn.GetSeq()
	tb.rw.Unlock()
	if !loaded {
		ctn.Update(value, tb.exchanger, seq)
	}
}


func (tb *Table) KeepLive(key interface{}, option ...KeepLiveOption) bool {
	o := mergeKeepLiveOption(option)
	duration := tb.keepLiveDuration
	if o.Duration != nil {
		duration = *o.Duration
	}

	tb.rw.RLock()
	e := tb.mp[key]
	if e == nil {
		tb.rw.RUnlock()
		return false
	}
	ctn := e.Value.(*container)
	seq := ctn.GetSeq()
	tb.moveToEnd(e)
	tb.rw.RUnlock()
	ctn.KeepLive(duration, seq)
	return false
}

func (tb *Table) Delete(key Key) bool {
	tb.rw.Lock()
	e := tb.mp[key]
	if e == nil {
		tb.rw.Unlock()
		return false
	}
	ctn := e.Value.(*container)
	delete(tb.mp, key)
	tb.ll.Remove(e)
	seq := ctn.GetSeq()
	tb.rw.Unlock()

	return tb.releaseContainer(ctn, seq)
}

func (tb *Table) moveToEnd(e *list.Element) {
	tb.ll.MoveToBack(e)
}

func (tb *Table) load(key Key, data Value, keepLive time.Duration) (ctn *container, loaded bool) {
	e := tb.mp[key]
	if e == nil {
		ctn = tb.add(key, data, keepLive)
	} else {
		loaded = true
		ctn = e.Value.(*container)
		ctn.KeepLive(keepLive, ctn.GetSeq())
		tb.moveToEnd(e)
	}
	return
}

func (tb *Table) add(key Key, data Value, keepLive time.Duration) *container {
	ctn := tb.newContainer(key, data)
	e := tb.ll.PushBack(ctn)
	tb.mp[key] = e

	if tb.maxEntry != 0 {
		for tb.ll.Len() > tb.maxEntry {
			releaseItem := tb.ll.Front()
			if releaseItem != nil {
				releaseCtn := releaseItem.Value.(*container)
				releaseKey := releaseCtn.key
				releaseSeq := releaseCtn.GetSeq()
				go func() {
					tb.releaseContainer(releaseCtn, releaseSeq)
				}()
				delete(tb.mp, releaseKey)
				tb.ll.Remove(releaseItem)
			}
		}
	}
	if keepLive > 0 {
		ctn.KeepLive(keepLive, ctn.GetSeq())
	}
	return ctn
}

func (tb *Table) deleteBySeq(key interface{}, seq uint64) bool {
	tb.rw.Lock()
	e := tb.mp[key]
	if e == nil {
		tb.rw.Unlock()
		return false
	}
	ctn := e.Value.(*container)
	if ctn.seq != seq {
		tb.rw.Unlock()
		return false
	}
	delete(tb.mp, key)
	tb.ll.Remove(e)
	tb.rw.Unlock()

	return tb.releaseContainer(ctn, seq)
}

func (tb *Table) releaseContainer(ctn *container, seq uint64) bool {
	if ctn.Release(tb.exchanger, seq) {
		ctnPool.Put(ctn)
		return true
	}
	return false
}


func (tb *Table) newContainer(key Key, value Value) *container{
	ctn := ctnPool.Get().(*container)
	tb.seq ++
	ctn.seq = tb.seq
	ctn.data = value
	ctn.key = key
	ctn.closed = false
	if ctn.timer != nil {
		ctn.timer.Stop()
	}
	ctn.table = tb
	return ctn
}

type container struct {
	key interface{}
	rw sync.RWMutex
	liveTime time.Time
	timer *time.Timer
	data Value
	closed bool
	table *Table
	seq uint64
}

func (c *container) GetSeq() uint64 {
	c.rw.RLock()
	defer c.rw.RUnlock()
	return c.seq
}

func (c *container) Update(v Value, exchanger Exchanger, seq uint64) bool {
	c.rw.Lock()
	defer c.rw.Unlock()
	if c.seq != seq {
		return false
	}

	if c.closed {
		return false
	}

	exchanger(c.key, v, c.data, false)
	return true
}

func (c *container) GetData(now time.Time, seq uint64) (interface{}, bool) {
	c.rw.RLock()
	defer c.rw.RUnlock()
	var data = c.data
	if c.seq != seq {
		return nil, false
	}

	if c.isTimeout(now) {
		return nil, false
	}

	if c.closed {
		return nil, false
	}
	return data, true
}

func (c *container) KeepLive(duration time.Duration, seq uint64) bool {
	c.rw.Lock()
	defer c.rw.Unlock()

	if c.seq != seq {
		return false
	}

	if c.closed {
		return false
	}

	if c.timer != nil {
		c.timer.Stop()
	}
	seq = c.seq
	if duration != 0 {
		c.liveTime = time.Now().Add(duration)
		c.timer = time.AfterFunc(duration, func() {
			if c.IsTimeout(time.Now()) {
				c.table.deleteBySeq(c.key, seq)
			}
		})
	} else {
		c.liveTime = time.Time{}
	}

	return false
}

func (c *container) isTimeout(now time.Time) bool {
	if !c.liveTime.IsZero() && !c.liveTime.After(now){
		return true
	}
	return false
}

func (c *container) IsTimeout(now time.Time) bool {
	c.rw.RLock()
	defer c.rw.RUnlock()
	return c.isTimeout(now)
}


func (c *container) release(exchanger Exchanger, seq uint64) bool {
	if c.seq != seq {
		return false
	}

	if c.closed {
		return false
	}

	key := c.key
	value := c.data

	if c.timer != nil {
		c.timer.Stop()
	}

	exchanger(key, nil, value, true)
	c.closed = true

	return true
}

func (c *container) Release(exchanger Exchanger, seq uint64) bool {
	c.rw.Lock()
	defer c.rw.Unlock()
	return c.release(exchanger, seq)
}

var ctnPool = &sync.Pool{
	New: func() interface{} {
		return &container {

		}
	},
}

type SetOption struct {
	KeepLiveDuration *time.Duration
}

func (opt SetOption) SetKeepLive(duration time.Duration) SetOption {
	ret := opt
	ret.KeepLiveDuration = &duration
	return ret
}

func mergeSetOption(option []SetOption) SetOption {
	ret := SetOption{}
	for _, o := range option {
		if o.KeepLiveDuration != nil {
			ret.KeepLiveDuration = o.KeepLiveDuration
		}
	}
	return ret
}

type KeepLiveOption struct {
	Duration *time.Duration
}

func (opt KeepLiveOption) SetDuration(duration time.Duration) KeepLiveOption {
	ret := opt
	ret.Duration = &duration
	return ret
}

func mergeKeepLiveOption(option []KeepLiveOption) KeepLiveOption {
	ret := KeepLiveOption{}
	for _, o := range option {
		if o.Duration != nil {
			ret.Duration = o.Duration
		}
	}
	return ret
}
