import { act, createElement } from 'react';
import { createRoot } from 'react-dom/client';
import { useRef } from 'react';
import { expect,it,vi } from 'vitest';
import { shortcutKey } from './shortcuts';
import { useShortcuts } from './use-shortcuts';
it('accepts only the five unmodified dashboard keys and the question-mark chord',()=>{
  for(const key of ['r','e','h','f','?'])expect(shortcutKey(new KeyboardEvent('keydown',{key}),false)).toBe(key);
  expect(shortcutKey(new KeyboardEvent('keydown',{key:'?',shiftKey:true}),false)).toBe('?');
  for(const init of [{ctrlKey:true},{metaKey:true},{altKey:true},{shiftKey:true},{repeat:true}])expect(shortcutKey(new KeyboardEvent('keydown',{key:'r',...init}),false)).toBeNull();
  for(const key of ['/', 'Escape','R','a'])expect(shortcutKey(new KeyboardEvent('keydown',{key}),false)).toBeNull();
  expect(shortcutKey(new KeyboardEvent('keydown',{key:'r'}),true)).toBeNull();
  const prevented=new KeyboardEvent('keydown',{key:'r',cancelable:true});prevented.preventDefault();expect(shortcutKey(prevented,false)).toBeNull();
});
it.each(['','plaintext-only'])('ignores contenteditable=%j',value=>{
  const target=document.createElement('div');target.setAttribute('contenteditable',value);const event=new KeyboardEvent('keydown',{key:'r'});
  Object.defineProperty(event,'target',{value:target});expect(shortcutKey(event,false)).toBeNull();
});
it.each(['input','textarea','select','[contenteditable="true"]','[role="textbox"]','[role="dialog"]','[role="menu"]'])('ignores %s and its descendants',selector=>{
  const parent=document.createElement(selector.startsWith('[')?'div':selector);
  if(selector.startsWith('[')){const [,key,value]=/\[(.+)="(.+)"\]/.exec(selector)!;parent.setAttribute(key,value);}
  const target=document.createElement('span');parent.append(target);
  const event=new KeyboardEvent('keydown',{key:'r',bubbles:true});
  let result: string|null='not checked';parent.addEventListener('keydown',e=>{result=shortcutKey(e,false);});target.dispatchEvent(event);
  expect(result).toBeNull();
});
it('acts once only inside the active region, excludes fragments and overlays, and removes its listener',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);const run=vi.fn();
  function Host(){const region=useRef<HTMLDivElement>(null);useShortcuts(region,{r:run,f:run});return createElement('div',{ref:region},createElement('button',{},'Panel'),createElement('div',{'data-dashboard-fragment':true},createElement('button',{},'Chat panel')));}
  try{
    await act(async()=>root.render(createElement(Host)));
    const button=el.querySelector('button')!;button.focus();
    await act(async()=>button.dispatchEvent(new KeyboardEvent('keydown',{key:'r',bubbles:true,cancelable:true})));
    expect(run).toHaveBeenCalledOnce();
    document.dispatchEvent(new KeyboardEvent('keydown',{key:'r',bubbles:true}));expect(run).toHaveBeenCalledOnce();
    const chat=el.querySelectorAll('button')[1];chat.focus();chat.dispatchEvent(new KeyboardEvent('keydown',{key:'r',bubbles:true}));expect(run).toHaveBeenCalledOnce();
    const modal=document.createElement('div');modal.setAttribute('role','dialog');document.body.append(modal);
    button.focus();button.dispatchEvent(new KeyboardEvent('keydown',{key:'f',bubbles:true}));expect(run).toHaveBeenCalledOnce();modal.remove();
    await act(async()=>root.unmount());button.dispatchEvent(new KeyboardEvent('keydown',{key:'r',bubbles:true}));expect(run).toHaveBeenCalledOnce();
  }finally{el.remove();}
});
it('allows only help, exit and refresh in its full-screen portal and blocks every key under another overlay',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);
  const run=vi.fn(),region={current:el};
  function Host(){useShortcuts(region,{r:()=>run('r'),e:()=>run('e'),h:()=>run('h'),f:()=>run('f'),'?':()=>run('?')},{fullscreenScope:'board'});return createElement('button',{},'Inline');}
  const modal=document.createElement('div');modal.setAttribute('role','dialog');modal.setAttribute('data-panel-fullscreen','');modal.setAttribute('data-shortcut-scope','board');
  const target=document.createElement('button');modal.append(target);document.body.append(modal);
  try{
    await act(async()=>root.render(createElement(Host)));
    target.focus();for(const key of ['r','e','h','f','?'])target.dispatchEvent(new KeyboardEvent('keydown',{key,bubbles:true,cancelable:true}));
    expect(run.mock.calls.map(c=>c[0])).toEqual(['r','f','?']);
    const help=document.createElement('div');help.setAttribute('role','dialog');document.body.append(help);
    target.dispatchEvent(new KeyboardEvent('keydown',{key:'r',bubbles:true}));expect(run).toHaveBeenCalledTimes(3);help.remove();
  }finally{await act(async()=>root.unmount());el.remove();modal.remove();}
});
