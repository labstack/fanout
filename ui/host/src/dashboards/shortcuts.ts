export function shortcutKey(event:KeyboardEvent,modalOpen:boolean):string|null {
  const target=event.target;
  if(modalOpen || event.defaultPrevented || event.repeat || event.ctrlKey || event.metaKey || event.altKey || (event.shiftKey&&event.key!=='?'))return null;
  if(target instanceof Element && target.closest('input,textarea,select,[contenteditable="true"],[role="textbox"],[role="dialog"],[role="menu"]'))return null;
  return ['r','e','h','f','?'].includes(event.key)?event.key:null;
}

