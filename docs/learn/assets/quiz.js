// 共用測驗元件。用法：
//
//   <div class="quiz" data-answer="1">
//     <p class="q">問題？</p>
//     <div class="opts">
//       <button>選項一</button>
//       <button>選項二</button>
//     </div>
//     <p class="why" hidden>為什麼。</p>
//   </div>
//
// data-answer 是正確選項的索引，從 0 起算。
//
// 刻意的設計：答錯之後不鎖住其他選項，因為目標是提取練習不是考試；
// 但一旦作答，解釋就永遠顯示——看過答案再答一次沒有意義。

(function () {
  function wire(quiz) {
    var answer = Number(quiz.dataset.answer);
    var buttons = Array.prototype.slice.call(quiz.querySelectorAll('.opts button'));
    var why = quiz.querySelector('.why');
    var answered = false;

    buttons.forEach(function (button, index) {
      button.addEventListener('click', function () {
        if (button.classList.contains('right') || button.classList.contains('wrong')) return;
        button.classList.add(index === answer ? 'right' : 'wrong');
        if (index !== answer) {
          // 選錯時把正確答案一併標出來，否則使用者得自己猜哪個才對。
          buttons[answer].classList.add('right');
        }
        buttons.forEach(function (other) {
          if (!other.classList.contains('right') && !other.classList.contains('wrong')) {
            other.disabled = true;
          }
        });
        if (why) why.hidden = false;
        if (!answered) {
          answered = true;
          quiz.dispatchEvent(new CustomEvent('quiz:answered', {
            bubbles: true,
            detail: { correct: index === answer }
          }));
        }
      });
    });
  }

  function track(scoreboard, total) {
    var right = 0;
    var done = 0;
    function render() {
      scoreboard.textContent = done < total
        ? '已作答 ' + done + ' / ' + total + '，答對 ' + right
        : '作答完畢：' + right + ' / ' + total + (right === total ? ' — 全對。' : '');
    }
    document.addEventListener('quiz:answered', function (event) {
      done += 1;
      if (event.detail.correct) right += 1;
      render();
    });
    render();
  }

  document.addEventListener('DOMContentLoaded', function () {
    var quizzes = Array.prototype.slice.call(document.querySelectorAll('.quiz[data-answer]'));
    quizzes.forEach(wire);
    var scoreboard = document.querySelector('.score');
    if (scoreboard && quizzes.length) track(scoreboard, quizzes.length);
  });
})();
