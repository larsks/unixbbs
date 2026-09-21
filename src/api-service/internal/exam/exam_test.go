package exam

import (
	"os"
	"path/filepath"
	"testing"
)

var q1 = Question{Id: "TEST1", Correct: 0, CorrectLetter: "A", Question: "Is this a question?", Answers: []string{"Yes", "No"}}
var q2 = Question{Id: "TEST2", Correct: 2, CorrectLetter: "C", Question: "What is the answer?", Answers: []string{"100", "69", "42"}}
var q3 = Question{Id: "TEST3", Correct: 1, CorrectLetter: "B", Question: "Would you like fries with that?", Answers: []string{"No", "Yes"}}
var testPool = NewPool().WithQuestions([]Question{
	q1, q2,
})

func TestAddQuestion(t *testing.T) {
	len_before := len(testPool.Questions)
	testPool.AddQuestion(q3)
	len_after := len(testPool.Questions)

	if len_before == len_after {
		t.Fatalf("failed to add question")
	}
}

func TestAddDuplicateQuestion(t *testing.T) {
	len_before := len(testPool.Questions)
	testPool.AddQuestion(q1)
	len_after := len(testPool.Questions)
	if len_before != len_after {
		t.Fatalf("duplicate question should not be added")
	}
}

func TestQuestionExists(t *testing.T) {
	_, err := testPool.GetQuestionByID("TEST1")
	if err != nil {
		t.Fatalf("failed to get question from pool")
	}
}

func TestQuestionNotExists(t *testing.T) {
	_, err := testPool.GetQuestionByID("TESTX")
	if err == nil {
		t.Fatalf("failed to raise error for unknown question id")
	}
}

func TestLoadQuestionsFromFileConvertsToASCII(t *testing.T) {
	const data = `[
		{
			"id": "TEST1",
			"correct": 0,
			"correct_letter": "A",
			"refs": "“Section 1–2”",
			"question": "What’s the answer… really?",
			"answers": ["Yes", "No"]
		}
	]`

	path := filepath.Join(t.TempDir(), "questions.json")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	questions, err := LoadQuestionsFromFile(path)
	if err != nil {
		t.Fatalf("failed to load questions: %v", err)
	}
	if questions.Length() != 1 {
		t.Fatalf("expected 1 question, got %d", questions.Length())
	}

	q := questions[0]
	if q.Question != "What's the answer... really?" {
		t.Fatalf("question was not converted to ASCII: %q", q.Question)
	}
	if q.Refs != "\"Section 1-2\"" {
		t.Fatalf("refs was not converted to ASCII: %q", q.Refs)
	}
}
