"""Run serial on-device qualification. This is not part of model-free CI."""
import argparse
import json
import pathlib
import statistics
import subprocess
import time


def invoke(probe, output, label, request):
    start = time.monotonic()
    try:
        proc = subprocess.run(
            [str(probe)], input=json.dumps(request), text=True,
            capture_output=True, timeout=60)
        result = dict(request=request, stdout=proc.stdout, stderr=proc.stderr,
                      exit_code=proc.returncode)
    except subprocess.TimeoutExpired:
        result = dict(request=request, stdout="", stderr="timeout",
                      exit_code=None)
    result["wall_s"] = time.monotonic() - start
    result["events"] = [json.loads(line) for line in result["stdout"].splitlines()]
    (output / (label + ".json")).write_text(
        json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    return result


def function(name, description, properties):
    return dict(name=name, description=description, parameters=dict(
        type="object", properties=properties, required=list(properties),
        additionalProperties=False))


def terminal(result):
    if result["exit_code"] != 0:
        raise ValueError("probe failed")
    events = result["events"]
    errors = [e for e in events if e["event"] == "error"]
    if errors:
        raise ValueError(errors[0]["message"])
    return next(e for e in events if e["event"] == "transcript")


def add_results(request, calls, results):
    if request["prompt"]:
        request["history"].append(dict(type="user", text=request["prompt"]))
    request["history"].append(dict(type="calls", calls=calls))
    for call, text in zip(calls, results):
        request["history"].append(dict(
            type="output", id=call["id"], name=call["name"], text=text))
    request["prompt"] = ""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--probe", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    seeds = [11, 23, 37, 53, 71] + list(range(101, 116))
    weather = function("get_weather", "Get current observed weather for a city.",
                       {"city": dict(type="string")})
    gates = []
    for seed in seeds:
        request = dict(
            instructions="Use weather tools for current observations. Never invent weather. Give a concise answer based on the returned observations.",
            prompt="What's the current weather in Vienna and Berlin? Check both cities.",
            tools=[weather], history=[], seed=seed, required=False)
        captured = invoke(args.probe, args.output, f"batch-{seed}", request)
        event = terminal(captured)
        assert event["sentinel"], "no sentinel"
        assert len(event["batches"]) == 1, "not exactly one batch"
        calls = event["batches"][0]
        assert len(calls) == 2, "incomplete multi-call batch"
        assert len({c["id"] for c in calls}) == 2, "duplicate IDs"
        assert {json.loads(c["arguments"])["city"] for c in calls} == {"Vienna", "Berlin"}
        assert event["entry_kinds"] == ["prompt", "calls"], "partial or unexpected entries"
        results = []
        for call in calls:
            city = json.loads(call["arguments"])["city"]
            temp, condition = {"Vienna": (17, "rain"), "Berlin": (9, "snow")}[city]
            results.append(json.dumps(dict(city=city, temperature_c=temp, condition=condition)))
        add_results(request, calls, results)
        resumed = invoke(args.probe, args.output, f"resume-{seed}", request)
        continuation = terminal(resumed)
        assert not continuation["sentinel"] and not continuation["batches"], "repeated calls"
        answer = next(e["text"] for e in resumed["events"] if e["event"] == "complete")
        assert all(v in answer.lower() for v in ["vienna", "berlin", "17", "9", "rain", "snow"]), answer
        gates.append(dict(seed=seed, capture=True, resume=True))
        print(json.dumps(dict(gate="batch_resume", seed=seed, passed=True)), flush=True)
    rounds = []
    for decisions in [3, 5, 8]:
        for seed in seeds:
            tools = [function(
                f"lookup_stage_{i}",
                f"Retrieve stage {i} using the exact key from " +
                ("the user." if i == 1 else f"stage {i-1}."),
                {"key": dict(type="string")}) for i in range(1, decisions)]
            request = dict(
                instructions="Complete the requested dependent lookup chain. Each stage requires the exact key returned by the preceding stage. Never guess a key. Call one stage at a time, in order. After the last stage return its final code without more calls.",
                prompt=f"Run lookup_stage_1 through lookup_stage_{decisions-1} in order. The initial key is START. Each lookup returns the key for the next lookup. Report the final code from the last lookup.",
                tools=tools, history=[], seed=seed, required=False)
            start = time.monotonic()
            tokens = []
            error = None
            expected_key = "START"
            try:
                for step in range(1, decisions + 1):
                    result = invoke(args.probe, args.output,
                                    f"rounds-{decisions}-{seed}-{step}", request)
                    tokens.extend(e["input_tokens"] for e in result["events"]
                                  if e["event"] == "preflight")
                    event = terminal(result)
                    if step == decisions:
                        assert not event["sentinel"], "extra decision required"
                        answer = next(e["text"] for e in result["events"] if e["event"] == "complete")
                        assert "FINAL-731" in answer, "final answer not grounded"
                        break
                    assert event["sentinel"] and len(event["batches"]) == 1, "missing call"
                    calls = event["batches"][0]
                    assert len(calls) == 1, "speculative or duplicate calls"
                    call = calls[0]
                    assert call["name"] == f"lookup_stage_{step}", "wrong stage"
                    assert json.loads(call["arguments"]) == {"key": expected_key}, "wrong key"
                    expected_key = f"K-{seed}-{step}-Q9"
                    text = json.dumps(
                        {"final_code": "FINAL-731"} if step == decisions - 1
                        else {"next_key": expected_key})
                    add_results(request, calls, [text])
            except (AssertionError, ValueError, StopIteration, KeyError) as exc:
                error = str(exc) or type(exc).__name__
            row = dict(decisions=decisions, seed=seed, success=error is None,
                       max_input_tokens=max(tokens, default=0),
                       headroom=8192 - max(tokens, default=0),
                       latency_s=time.monotonic() - start, error=error)
            rounds.append(row)
            print(json.dumps(row), flush=True)
    summaries = []
    for count in [3, 5, 8]:
        rows = [r for r in rounds if r["decisions"] == count]
        summaries.append(dict(
            decisions=count, passed=sum(r["success"] for r in rows), total=len(rows),
            max_input_tokens=max(r["max_input_tokens"] for r in rows),
            min_headroom=min(r["headroom"] for r in rows),
            median_s=statistics.median(r["latency_s"] for r in rows),
            p95_s=sorted(r["latency_s"] for r in rows)[18]))
    qualified = [r["decisions"] for r in summaries
                 if r["passed"] / r["total"] >= .95 and r["min_headroom"] >= 2048]
    summary = dict(gates=gates, rounds=rounds, summaries=summaries,
                   qualified_default=max(qualified, default=None))
    (args.output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(dict(summaries=summaries, qualified_default=summary["qualified_default"])), flush=True)


if __name__ == "__main__":
    main()
