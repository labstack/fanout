import {MODELS,CRITERIA,RUBRIC,SCHEMA} from './judge';
export function startJudgeMock(failure?:string) {
  const stats={requests:0,bodies:[] as any[],inputs:{anthropic:[] as string[],openai:[] as string[]},assertions:[] as string[]};
  const server=Bun.serve({hostname:'127.0.0.1',port:0,async fetch(req){
    stats.requests++;
    const path=new URL(req.url).pathname,provider=path==='/v1/messages'?'anthropic':'openai';
    const body=await req.json();stats.bodies.push(body);stats.inputs[provider].push(body.messages?.at(-1)?.content);
    if(req.method!=='POST'||!['/v1/messages','/v1/chat/completions'].includes(path))stats.assertions.push('endpoint');
    if(body.model!==MODELS[provider])stats.assertions.push('model');
    if(provider==='anthropic') {
      if(req.headers.get('x-api-key')!=='mock-only-anthropic'||body.system!==RUBRIC||body.max_tokens!==1024||body.output_config?.format?.type!=='json_schema'||!body.output_config.format.schema.properties.answers_question.enum)stats.assertions.push('anthropic_schema');
    }else if(req.headers.get('authorization')!=='Bearer mock-only-openai'||body.messages[0]?.content!==RUBRIC||body.max_completion_tokens!==4096||JSON.stringify(body.response_format?.json_schema?.schema)!==JSON.stringify(SCHEMA)||body.response_format?.json_schema?.strict!==true)stats.assertions.push('openai_schema');
    if(provider==='anthropic') {
      if(failure==='http_unknown')return Response.json({error:{message:'mock-only-anthropic'}},{status:503});
      if(failure==='invalid_json')return new Response('not JSON');
      if(failure==='exhausted'||failure==='retry'&&stats.inputs.anthropic.length===1)return Response.json({error:{type:'rate_limit_error'}},{status:429});
    }else if(failure==='paid_temperature')return Response.json({error:{code:'unsupported_value',param:'temperature'},usage:{prompt_tokens:20,completion_tokens:10}},{status:400});
    else if(failure==='retry'&&'temperature' in body)return Response.json({error:{code:'unsupported_value',param:'temperature'}},{status:400});
    const rubric={...Object.fromEntries(CRITERIA.map(k=>[k,provider==='anthropic'?4:3])),rationale:'Deterministic synthetic judgment.'};
    if(provider==='openai') {
      rubric.answers_question=2;rubric.correct_fields_aggregations=4;
      if(failure==='invalid_score')rubric.answers_question=6;
      if(failure==='blank_rationale')rubric.rationale=' ';
      if(failure==='extra_key')(rubric as any).extra=true;
    }
    const text=provider==='openai'&&failure==='malformed'?'{bad':JSON.stringify(rubric);
    return Response.json(provider==='anthropic'
      ?{model:failure==='response_model'?'different-model':MODELS.anthropic,content:[{type:'text',text}],stop_reason:'end_turn',...(failure==='missing_usage'?{}:{usage:failure==='bad_usage'?{input_tokens:-1}:{input_tokens:10,output_tokens:5,cache_read_input_tokens:2,cache_creation_input_tokens:3}})}
      :{model:MODELS.openai,choices:[{message:{content:text,...(failure==='refusal'?{refusal:'Synthetic refusal'}:{})},finish_reason:failure==='truncated'?'length':'stop'}],usage:{prompt_tokens:20,completion_tokens:10,prompt_tokens_details:{cached_tokens:4},completion_tokens_details:{reasoning_tokens:6}}});
  }});
  return {server,base:`http://127.0.0.1:${server.port}`,stats};
}
