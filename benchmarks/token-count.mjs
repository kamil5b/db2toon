import process from "node:process";
import { countTokens as countAnthropicTokens } from "@anthropic-ai/tokenizer";
import { get_encoding } from "tiktoken";

let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) {
  input += chunk;
}

const encoding = get_encoding("cl100k_base");
try {
  process.stdout.write(JSON.stringify({
    openai: encoding.encode(input).length,
    anthropic: countAnthropicTokens(input),
  }));
} finally {
  encoding.free();
}
