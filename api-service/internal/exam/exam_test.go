package exam

import "testing"

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
