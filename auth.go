package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

func hashPassword(pass, salt string) string {
	sum := sha256.Sum256([]byte(salt + ":" + pass))
	return hex.EncodeToString(sum[:])
}

// parseAuthString 解析 Emby 授权串，返回 token 与 deviceId。
// 兼容: MediaBrowser Client="x", Token = "abc", DeviceId = "y"（含空格/引号）
// 以及 Emby Token="abc", DeviceId="y"。
func parseAuthString(s string) (token, device string) {
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		val := strings.Trim(strings.TrimSpace(kv[1]), "\"")
		switch key {
		case "token":
			token = val
		case "deviceid":
			device = val
		}
	}
	return
}

// tokenFromRequest 依次从 X-Emby-Token、api_key、授权头/查询参数中取令牌。
func tokenFromRequest(r *http.Request) string {
	if t := r.Header.Get("X-Emby-Token"); t != "" {
		return t
	}
	if t := r.URL.Query().Get("api_key"); t != "" {
		return t
	}
	if t := r.URL.Query().Get("X-Emby-Token"); t != "" {
		return t
	}
	for _, s := range []string{
		r.Header.Get("X-Emby-Authorization"),
		r.Header.Get("Authorization"),
		r.URL.Query().Get("X-Emby-Authorization"),
		r.URL.Query().Get("Authorization"),
	} {
		if s == "" {
			continue
		}
		if tok, _ := parseAuthString(s); tok != "" {
			return tok
		}
	}
	return ""
}

// userByToken 返回令牌对应的用户；空令牌或未知令牌返回 nil。
func (s *Store) userByToken(r *http.Request) *User {
	tok := tokenFromRequest(r)
	if tok == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	uid, ok := s.Tokens[tok]
	if !ok {
		return nil
	}
	return s.Users[uid]
}

// requireAuth 包裹需要登录的处理器。
func (s *Store) requireAuth(next func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.userByToken(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "认证失败：缺少或无效的访问令牌")
			return
		}
		next(w, r, u)
	}
}

func (s *Store) requireAdmin(next func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, u *User) {
		if !u.IsAdmin {
			writeErr(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next(w, r, u)
	})
}

// authenticate 校验用户名/密码，成功返回用户；不存在或密码错返回 nil。
func (s *Store) authenticate(name, pass string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.Users {
		if strings.EqualFold(u.Name, name) {
			if !u.HasPassword {
				return u
			}
			if hashPassword(pass, u.Salt) == u.PassHash {
				return u
			}
			return nil
		}
	}
	return nil
}

// issueToken 为用户签发访问令牌。
func (s *Store) issueToken(u *User) string {
	tok := newID()
	s.mu.Lock()
	s.Tokens[tok] = u.Id
	s.mu.Unlock()
	go s.Save()
	return tok
}
