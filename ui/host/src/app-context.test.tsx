import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect,it,vi } from 'vitest';
import { useDashboardReceipts } from './app-context';
import { build,user } from '../tests/dashboard-receipts';
import type { Message } from '@ag-ui/client';
it('reuses finished-turn receipts without reparsing their tool payloads on streamed deltas',async()=>{
 const old=build();const current=[user('next')];let receipts:ReturnType<typeof useDashboardReceipts>;
 function Probe({messages}:{messages:Message[]}) {receipts=useDashboardReceipts(messages);return null;}
 const root=createRoot(document.createElement('div'));const parse=vi.spyOn(JSON,'parse');
 try{await act(async()=>root.render(<Probe messages={[...old,...current]}/>));const original=receipts!.get('u');parse.mockClear();
 await act(async()=>root.render(<Probe messages={[...old,...current,{id:'delta',role:'assistant',content:'Next answer'}]}/>));
 expect(receipts!.get('u')).toBe(original);expect(parse).not.toHaveBeenCalled();
 }finally{parse.mockRestore();await act(async()=>root.unmount());}
});
