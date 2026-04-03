package skill

import "sync"

// Registry 全局技能注册中心，负责技能的注册与查找
type Registry struct {
	mu     sync.RWMutex
	skills map[string]Skill
}

var (
	globalRegistry *Registry
	registryOnce   sync.Once
)

// GetRegistry 返回全局注册中心单例
func GetRegistry() *Registry {
	registryOnce.Do(func() {
		globalRegistry = &Registry{
			skills: make(map[string]Skill),
		}
	})
	return globalRegistry
}

// Register 注册一个技能；若 code 已存在则覆盖
func (r *Registry) Register(s Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.Code()] = s
}

// Get 根据 code 查找技能
func (r *Registry) Get(code string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[code]
	return s, ok
}

// All 返回当前所有已注册的技能列表（快照）
func (r *Registry) All() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		result = append(result, s)
	}
	return result
}

// Codes 返回所有已注册技能的 code 集合
func (r *Registry) Codes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	codes := make([]string, 0, len(r.skills))
	for code := range r.skills {
		codes = append(codes, code)
	}
	return codes
}
