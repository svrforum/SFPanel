package com.sfpanel.android;
import org.junit.Test;
import static org.junit.Assert.assertEquals;

public class TerminalKeysTest {
    @Test public void aiKeysHaveDistinctShiftSequences() {
        assertEquals("\u001b[Z", TerminalKeys.encode("\t", true, false, false));
        assertEquals("\u001b[13;2u", TerminalKeys.encode("\r", true, false, false));
        assertEquals("\u001b[1;6D", TerminalKeys.encode("\u001b[D", true, true, false));
    }
    @Test public void controlAndAltCompose() {
        assertEquals("\u0003", TerminalKeys.encode("c", false, true, false));
        assertEquals("\u001b\u0003", TerminalKeys.encode("c", false, true, true));
        assertEquals("\u001b[5;2~", TerminalKeys.encode("\u001b[5~", true, false, false));
    }
}
