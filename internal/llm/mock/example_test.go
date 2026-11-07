package mock

import (
	"context"
	"fmt"

	"cyberdigest/internal/llm"
)

func ExampleClient_When() {
	m := New()
	m.When("classify_topic", "ransomware", `{"on_topic":true}`)
	resp, err := m.Complete(context.Background(), llm.Request{
		Task: "classify_topic",
		User: "New ransomware note",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp.Text)
	fmt.Println(m.Calls())
	// Output:
	// {"on_topic":true}
	// 1
}
