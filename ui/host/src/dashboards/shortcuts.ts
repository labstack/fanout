export function shortcutKey(event:KeyboardEvent,modalOpen:boolean,fullscreen=false):string|null {
  const target=event.target;
  if(modalOpen || event.defaultPrevented || event.repeat || event.ctrlKey || event.metaKey || event.altKey || (event.shiftKey&&event.key!=='?'))return null;
  if(target instanceof Element && target.closest('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"],[role="menu"]'))return null;
  if(target instanceof Element && target.closest('[role="dialog"]') && !(fullscreen && target.closest('[data-panel-fullscreen]')))return null;
  return ['r','e','h','f','?'].includes(event.key)?event.key:null;
}

