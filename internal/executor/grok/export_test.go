package grok

// WriteServeInfoForTest 暴露 serve.json 写入，供 grok_test 包做往返断言。
func WriteServeInfoForTest(p *Proc) error { return writeServeInfo(p) }
