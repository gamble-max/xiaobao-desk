package com.xiaobao.desk.ui

import android.os.Bundle
import android.view.KeyEvent
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.EditText
import android.widget.ScrollView
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import com.xiaobao.desk.R
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch

class TerminalFragment : Fragment() {

    private lateinit var tvOutput: TextView
    private lateinit var svOutput: ScrollView
    private lateinit var etInput: EditText

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View = inflater.inflate(R.layout.fragment_terminal, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        val conn = (requireActivity() as MainActivity).connectionManager

        tvOutput = view.findViewById(R.id.tvTerminalOutput)
        svOutput = view.findViewById(R.id.svTerminalOutput)
        etInput = view.findViewById(R.id.etTerminalInput)

        view.findViewById<Button>(R.id.btnTermOpen).setOnClickListener {
            conn.terminalClient.open(120, 36)
        }
        view.findViewById<Button>(R.id.btnTermClear).setOnClickListener {
            tvOutput.text = ""
        }
        view.findViewById<Button>(R.id.btnTermCtrlC).setOnClickListener {
            conn.terminalClient.sendStdin(byteArrayOf(0x03))
        }

        val sendAction = {
            val cmd = etInput.text?.toString().orEmpty()
            if (cmd.isNotEmpty()) {
                conn.terminalClient.sendCommand(cmd + "\r\n")
                etInput.setText("")
            }
        }

        view.findViewById<Button>(R.id.btnTerminalSend).setOnClickListener { sendAction() }
        etInput.setOnEditorActionListener { _, actionId, event ->
            if (actionId == EditorInfo.IME_ACTION_SEND ||
                (event != null && event.keyCode == KeyEvent.KEYCODE_ENTER && event.action == KeyEvent.ACTION_DOWN)
            ) {
                sendAction()
                true
            } else {
                false
            }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            conn.terminalClient.textFlow.collectLatest { chunk ->
                tvOutput.append(chunk)
                svOutput.post { svOutput.fullScroll(View.FOCUS_DOWN) }
            }
        }
    }
}
