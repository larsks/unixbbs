package exam

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
)

type (
	Question struct {
		Id            string   `json:"id"`
		Correct       int      `json:"correct"`
		CorrectLetter string   `json:"correct_letter"`
		Refs          string   `json:"refs"`
		Question      string   `json:"question"`
		Answers       []string `json:"answers"`
		Figure        string   `json:"figure"`
	}

	Questions []Question

	Pool struct {
		Questions
		Index map[string]Question
	}

	Examiner struct {
		Pools   map[string]*Pool
		dataDir string
	}
)

func LoadQuestionsFromFile(path string) (Questions, error) {
	fd, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open pool file: %w", err)
	}
	content, err := io.ReadAll(fd)
	if err != nil {
		return nil, fmt.Errorf("failed to read pool file: %w", err)
	}

	var questions Questions
	if err := json.Unmarshal(content, &questions); err != nil {
		return nil, fmt.Errorf("failed to parse json: %w", err)
	}

	return questions, nil
}

func (q Questions) RandomQuestion() *Question {
	if q.Length() == 0 {
		return nil
	}
	index := rand.IntN(q.Length())
	return &q[index]
}

func (q Questions) RandomQuestionWithoutFigure() *Question {
	for {
		rq := q.RandomQuestion()
		if rq == nil {
			return nil
		}
		if rq.Figure != "" {
			continue
		}
		return rq
	}
}

func (q Questions) Length() int {
	return len(q)
}

func NewPool() *Pool {
	return &Pool{
		Questions: Questions{},
		Index:     make(map[string]Question),
	}
}

func (p *Pool) WithQuestions(questions Questions) *Pool {
	for _, q := range questions {
		p.AddQuestion(q)
	}

	return p
}

func (p *Pool) AddQuestion(q Question) {
	_, exists := p.Index[q.Id]
	if !exists {
		p.Questions = append(p.Questions, q)
		p.Index[q.Id] = q
	}
}

func (p *Pool) GetQuestionByID(id string) (*Question, error) {
	if q, ok := p.Index[id]; ok {
		return &q, nil
	}

	return nil, fmt.Errorf("no question found for id %s", id)
}

func NewExaminer(dataDir string) *Examiner {
	return &Examiner{
		Pools:   make(map[string]*Pool),
		dataDir: dataDir,
	}
}

func (x *Examiner) LoadQuestionPools() error {
	files, err := os.ReadDir(x.dataDir)
	if err != nil {
		return fmt.Errorf("failed to load question pools: %w", err)
	}

	for _, entry := range files {
		if !entry.IsDir() {
			continue
		}

		poolName := entry.Name()
		questionsPath := filepath.Join(x.dataDir, entry.Name(), "questions.json")
		log.Printf("loading questions for %s from %s", poolName, questionsPath)
		questions, err := LoadQuestionsFromFile(questionsPath)
		if err != nil {
			return fmt.Errorf("failed to load pool %s: %w", poolName, err)
		}
		log.Printf("found %d questions in %s", questions.Length(), poolName)
		x.Pools[poolName] = NewPool().WithQuestions(questions)
	}

	return nil
}

func (x *Examiner) GetPool(name string) (*Pool, error) {
	pool, exists := x.Pools[name]
	if !exists {
		return nil, fmt.Errorf("no such pool: %s", name)
	}

	return pool, nil
}

func (x *Examiner) ListPools() []string {
	return slices.Collect(maps.Keys(x.Pools))
}
