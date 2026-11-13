package workbuddy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"sync"
)

const chatStrategyLimit = 256

type chatStrategyKey struct {
	site, account, model, system string
}

type chatStrategy struct {
	comma, system, afterTools bool
}

type chatCompatibility struct {
	mu      sync.Mutex
	entries map[chatStrategyKey]chatStrategy
	order   []chatStrategyKey
	next    int
}

func (c *chatCompatibility) strategy(key chatStrategyKey) chatStrategy {
	c.mu.Lock()
	defer c.mu.Unlock()
	value := c.entries[key]
	key.system = ""
	value.afterTools = c.entries[key].afterTools
	return value
}

func (c *chatCompatibility) remember(key chatStrategyKey, value chatStrategy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	put := func(key chatStrategyKey, value chatStrategy) {
		if c.entries == nil {
			c.entries = make(map[chatStrategyKey]chatStrategy)
			c.order = make([]chatStrategyKey, 0, chatStrategyLimit)
		}
		if _, exists := c.entries[key]; !exists {
			if len(c.order) == chatStrategyLimit {
				delete(c.entries, c.order[c.next])
				c.order[c.next] = key
				c.next = (c.next + 1) % chatStrategyLimit
			} else {
				c.order = append(c.order, key)
			}
		}
		c.entries[key] = value
	}
	if key.system != "" && (value.comma || value.system) {
		stored := c.entries[key]
		stored.comma = stored.comma || value.comma
		stored.system = stored.system || value.system
		put(key, stored)
	}
	if value.afterTools {
		key.system = ""
		put(key, chatStrategy{afterTools: true})
	}
}

func (c *chatCompatibility) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	c.order = nil
	c.next = 0
}

func (prepared *preparedChat) applyStrategy(strategy chatStrategy) error {
	if strategy.system {
		changed, err := moveChatSystem(prepared.body)
		if err != nil {
			return err
		}
		prepared.tried.system = changed
		prepared.strategy.system = changed
	}
	if strategy.comma {
		changed, system, err := commaChatBody(prepared.body)
		if err != nil {
			return err
		}
		prepared.tried.comma = changed
		prepared.strategy.comma = system
	}
	if strategy.afterTools {
		changed, err := replyChatAfterTools(prepared.body)
		if err != nil {
			return err
		}
		prepared.tried.afterTools = changed
		prepared.strategy.afterTools = changed
	}
	return nil
}

func (prepared *preparedChat) correct(refused, afterTools bool) (bool, error) {
	if prepared.messages {
		return false, nil
	}
	if refused && !prepared.tried.comma {
		prepared.tried.comma = true
		changed, system, err := commaChatBody(prepared.body)
		if err != nil || changed {
			prepared.strategy.comma = system
			return changed, err
		}
	}
	if refused && !prepared.tried.system {
		prepared.tried.system = true
		changed, err := moveChatSystem(prepared.body)
		if err != nil || changed {
			prepared.strategy.system = changed
			return changed, err
		}
	}
	if afterTools && !prepared.tried.afterTools {
		prepared.tried.afterTools = true
		changed, err := replyChatAfterTools(prepared.body)
		if err != nil || changed {
			prepared.strategy.afterTools = changed
			return changed, err
		}
	}
	return false, nil
}

func (prepared *preparedChat) encodeBody() error {
	payload, err := json.Marshal(prepared.body)
	if err != nil {
		return failure("invalid_request", "cannot encode WorkBuddy request", 400)
	}
	if len(payload) > maxChatPayload {
		return failure("request_too_large", "normalized WorkBuddy payload exceeds the 32 MiB limit", 413)
	}
	if prepared.messages {
		prepared.request.Body = payload
		return nil
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		return failure("internal_error", "cannot compress chat payload", 500)
	}
	if err := writer.Close(); err != nil {
		return failure("internal_error", "cannot finish compressed chat payload", 500)
	}
	prepared.request.Body = compressed.Bytes()
	return nil
}
