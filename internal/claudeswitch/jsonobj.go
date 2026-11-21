package claudeswitch

import (
	"bytes"
	"encoding/json"
	"errors"
)

// object 是保留键序的 JSON 对象，值原样保留为 JSON 文本：换号只搬动顶层字段，不改写里面的值。
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *object { return &object{vals: map[string]json.RawMessage{}} }

var errNotObject = errors.New("不是 JSON 对象")

// parseObject 解析一个 JSON 对象；语法错误原样返回，合法 JSON 但不是对象时返回 errNotObject。
// 重复的键取最后一个值、留在第一次出现的位置。
func parseObject(data []byte) (*object, error) {
	if !json.Valid(data) {
		var probe any
		return nil, json.Unmarshal(data, &probe)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errNotObject
	}
	o := newObject()
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.set(tok.(string), raw)
	}
	return o, nil
}

func (o *object) set(key string, value json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = value
}

func (o *object) get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *object) len() int { return len(o.keys) }

// filter 按键挑出一部分，键序不变。
func (o *object) filter(keep func(string) bool) *object {
	out := newObject()
	for _, k := range o.keys {
		if keep(k) {
			out.set(k, o.vals[k])
		}
	}
	return out
}

// merged 是 o 与 other 合并的结果：other 的键覆盖同名值（位置不动），新键接在后面。
func (o *object) merged(other *object) *object {
	out := newObject()
	for _, src := range []*object{o, other} {
		for _, k := range src.keys {
			out.set(k, src.vals[k])
		}
	}
	return out
}

// child 取值为对象的字段；没有这个字段时是空对象，值不是对象时报错。
func (o *object) child(key string) (*object, error) {
	raw, ok := o.get(key)
	if !ok {
		return newObject(), nil
	}
	return parseObject(raw)
}

func (o *object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// field 是写 JSON 时的一个有序字段。
type field struct {
	key   string
	value any
}

// fields 按给定顺序写成 JSON 对象。
type fields []field

func (fs fields) MarshalJSON() ([]byte, error) {
	o := newObject()
	for _, f := range fs {
		raw, err := marshal(f.value)
		if err != nil {
			return nil, err
		}
		o.set(f.key, raw)
	}
	return o.MarshalJSON()
}

// pretty 是两格缩进的 JSON，结尾不带换行。
func pretty(v any) ([]byte, error) {
	compact, err := marshal(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// decode 把 JSON 文本解成通用值；坏的文本当作 null。
func decode(raw json.RawMessage) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

// str 是字符串字段的值；缺省、null 或不是字符串时为空串。
func str(raw json.RawMessage) string {
	s, _ := decode(raw).(string)
	return s
}

// text 是 JSON 值放进说明文字时的样子：字符串取原文，其余用 JSON 写法。
func text(raw json.RawMessage) string {
	if s, ok := decode(raw).(string); ok {
		return s
	}
	return string(raw)
}

// marshal 把 v 编码成紧凑 JSON，不转义 <、>、&，结尾不带换行。
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
